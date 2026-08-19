// Jail lifecycle (C1): one type owns the full per-request sandbox - the
// unprivileged uid, the jail directory + chown, the cgroup v2 leaf, the
// source file, the build artifact, and teardown. Both the sequential and the
// parallel paths of ExecuteRun build on it: sequential materializes one jail
// and reuses it for every test; parallel materializes one build template and
// seeds a fresh jail per test from it (source + artifact copied).
//
// Before this module the lifecycle was forked across the two paths, which hid
// three defects: parallel jails never received the build artifact, the uid
// pool could be exhausted by parallel requests, and parallel cgroup names
// (`par-<pid>-<idx>`) collided across concurrent requests.
package runner

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/nithitsuki/goboxd/internal/cgroupv2"
	"github.com/nithitsuki/goboxd/internal/config"
	"github.com/nithitsuki/goboxd/internal/models"
)

// jail is one materialized sandbox: an unprivileged uid, a 0700 jail
// directory owned by that uid, a cgroup v2 leaf, and the user's source. The
// build artifact lands in the app dir after the build step. teardown releases
// all of it in one guaranteed path.
type jail struct {
	dir string
	uid int
	cg  *cgroupv2.Jail
	// seccomp holds the per-language ADDITIONAL deny syscalls (P2-12) carved
	// from lc.Seccomp at materialization. Empty = this jail uses the global
	// --seccomp_policy file; non-empty = nsjail gets --seccomp_string with the
	// combined (global + extras) inline policy.
	seccomp string

	once sync.Once // teardown runs exactly once, even under concurrent calls
}

// newJail materializes a jail: allocates the uid, creates and chowns the jail
// dir, creates the cgroup leaf (named from the unique jail dir basename, so
// concurrent requests can never collide - the old `par-<pid>-<idx>` scheme
// collided because os.Getpid() is constant per process), creates the app dir,
// and writes the source. On any error it releases everything allocated so far,
// including the partially-created cgroup dir when NewJail fails after creating
// it, and returns the error.
func newJail(req models.RunRequest, lc config.LanguageConfig, srcName string) (*jail, error) {
	// Never share a uid between jails: an allocation failure is an
	// infrastructure error, not a silent fallback.
	uid, err := uidPool.Alloc()
	if err != nil {
		return nil, fmt.Errorf("allocating jail uid: %w", err)
	}

	// os.MkdirTemp guarantees unique, non-colliding directories (Security
	// Hole #5: uid collisions).
	dir, err := os.MkdirTemp("", "goboxd-jail-*")
	if err != nil {
		uidPool.Release(uid)
		return nil, fmt.Errorf("failed to create jail dir: %w", err)
	}

	// The jail dir starts root-owned 0700; hand it to this jail's uid so the
	// jailed process can read/write its own files while other uids (other
	// jails) cannot even traverse it. The runner (root) keeps access for
	// cleanup and artifact reads.
	if err := os.Chown(dir, uid, uid); err != nil {
		uidPool.Release(uid)
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("failed to chown jail dir: %w", err)
	}

	j := &jail{dir: dir, uid: uid, seccomp: lc.Seccomp}

	// Per-jail cgroup v2 directory (memory+pids limits enforced by nsjail
	// inside it). The name is filepath.Base(dir) - unique by construction, so
	// concurrent parallel requests never collide. Any setup failure degrades
	// to the rlimit path for this request - limits stay enforced either way.
	if cgroupv2.Default().Active() {
		cg, err := cgroupv2.Default().NewJail(filepath.Base(dir))
		if err != nil {
			// NewJail may have created the jail dir before failing to enable
			// its controllers; no *Jail was returned, so teardown cannot
			// remove it. Clean up the partial dir best-effort (no stale
			// cgroup dirs, Hole 7).
			_ = cgroupv2.Default().RemoveJail(filepath.Base(dir))
			log.Printf("[runner] cgroup jail setup failed, using rlimit fallback for this request: %v", err)
			cg = nil
		}
		j.cg = cg
	}

	app := j.appDir()
	if err := os.MkdirAll(app, 0755); err != nil {
		j.teardown()
		return nil, fmt.Errorf("failed to create app dir: %w", err)
	}
	if err := os.Chown(app, uid, uid); err != nil {
		j.teardown()
		return nil, fmt.Errorf("failed to chown app dir: %w", err)
	}
	srcPath := filepath.Join(app, srcName)
	if err := writeSource(srcPath, []byte(req.Source)); err != nil {
		j.teardown()
		return nil, fmt.Errorf("failed to write source: %w", err)
	}
	if err := os.Chown(srcPath, uid, uid); err != nil {
		j.teardown()
		return nil, fmt.Errorf("failed to chown source: %w", err)
	}
	return j, nil
}

// appDir returns the app directory inside the jail (bind-mounted as /app).
func (j *jail) appDir() string {
	return filepath.Join(j.dir, "app")
}

// seedFrom copies the template jail's app dir contents (source AND build
// artifact) into this jail's app dir, chowning every copied entry to this
// jail's uid so its jailed process can read and execute them. Parallel tests
// are materialized from the build template this way: without the copy the
// compiled artifact would never reach the fresh jail dirs.
func (j *jail) seedFrom(t *jail) error {
	return copyTree(t.appDir(), j.appDir(), j.uid)
}

// teardown releases everything the jail holds - the uid, the jail dir, the
// cgroup leaf - in one guaranteed path. It is idempotent and race-free under
// concurrent calls: sync.Once guarantees exactly one teardown runs and every
// other caller is a no-op. It never fails: leftover dirs and cgroup leaves are
// cleaned by the startup sweeps.
func (j *jail) teardown() {
	if j == nil {
		return
	}
	j.once.Do(func() {
		uidPool.Release(j.uid)
		if j.cg != nil {
			j.cg.Teardown()
		}
		if j.dir != "" {
			if err := os.RemoveAll(j.dir); err != nil {
				fmt.Fprintf(os.Stderr, "failed to remove jail dir: %v\n", err)
			}
		}
	})
}

// copyTree copies every entry under srcDir into dstDir (which must already
// exist), preserving permissions and ownership per uid. Directories are
// recreated recursively; symlinks are recreated as symlinks (never followed,
// so a template cannot redirect the copy outside the jail); regular files are
// copied and their mode restored (a compiled artifact carries +x). The source
// file seeded by newJail is replaced with the template's identical copy.
// Non-regular entries (FIFOs, sockets, devices) are skipped: opening a FIFO
// with no writer blocks forever, and a build step could run user code that
// creates one, so the copy must never block.
func copyTree(srcDir, dstDir string, uid int) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		src := filepath.Join(srcDir, e.Name())
		dst := filepath.Join(dstDir, e.Name())
		switch {
		case e.Type().IsDir():
			info, err := e.Info()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
				return err
			}
			if err := os.Chown(dst, uid, uid); err != nil {
				return err
			}
			if err := copyTree(src, dst, uid); err != nil {
				return err
			}
		case e.Type()&os.ModeSymlink != 0:
			target, err := os.Readlink(src)
			if err != nil {
				return err
			}
			_ = os.Remove(dst)
			if err := os.Symlink(target, dst); err != nil {
				return err
			}
			if err := os.Lchown(dst, uid, uid); err != nil {
				return err
			}
		case e.Type().IsRegular():
			info, err := e.Info()
			if err != nil {
				return err
			}
			// The dst open must never follow a link planted at dst (mirroring
			// writeSource's O_NOFOLLOW|O_EXCL). newJail pre-seeds the source
			// file, so the dst entry is removed first; with O_EXCL, any entry
			// still present at dst after the remove fails this copy step
			// instead of being followed.
			_ = os.Remove(dst)
			// O_NONBLOCK is belt-and-suspenders against a regular file being
			// swapped for a FIFO between ReadDir and Open: opening it cannot
			// block, and a subsequent read returns EAGAIN instead of hanging.
			in, err := os.OpenFile(src, os.O_RDONLY|syscall.O_NONBLOCK, 0)
			if err != nil {
				return err
			}
			out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, info.Mode().Perm())
			if err != nil {
				_ = in.Close()
				return err
			}
			_, cErr := io.Copy(out, in)
			inErr := in.Close()
			outErr := out.Close()
			if cErr != nil {
				return cErr
			}
			if inErr != nil {
				return inErr
			}
			if outErr != nil {
				return outErr
			}
			if err := os.Chown(dst, uid, uid); err != nil {
				return err
			}
			if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
				return err
			}
		default:
			// FIFOs, sockets, devices: never copied. A build step that ran
			// user code could create a FIFO, and the copy must never block.
			log.Printf("[runner] copyTree: skipping non-regular entry %s (type %s)", src, e.Type())
		}
	}
	return nil
}
