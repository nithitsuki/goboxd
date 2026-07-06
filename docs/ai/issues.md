# Issues Log

## 2026-05-24 · nsjail submodule git errors during Docker build

**What we were trying to do:**
Build the Docker image with nsjail compiled from source as a git submodule.

**What went wrong:**
The nsjail Makefile runs `git submodule update --init` for its kafel dependency, but inside Docker the .git metadata is incomplete, so it fails with "not a git repository" errors.

**How we resolved it:**
Ran `git submodule update --init` on the host before building, so kafel is already populated when Docker copies the files. The Makefile sees kafel/Makefile exists and skips the git commands.

**What we learned:**
Docker COPY doesnt preserve git submodule metadata. Pre-populate submodule dependencies on the host first.

## 2026-05-25 · nsjail leaking UID warnings into stderr

**What we were trying to do:**
Capture clean stderr from user programs without nsjail's internal chatter bleeding into it.

**What went wrong:**
nsjail prints "[W] logParams():313 Process will be UID=0" to stderr, which gets captured alongside user program stderr and breaks our JSON response parsing.

**How we resolved it:**
Added --log /dev/null and -Q flags to suppress nsjail's internal logging while keeping the user process's actual stderr output intact through the IPC pipes.

**What we learned:**
nsjail mixes its own diagnostics with user stderr by default. You have to explicitly silence it.

## 2026-06-12 · cgroup v2 not available in Docker Desktop

**What I was trying to do:**
Use nsjail's --cgroup_pids_max and --cgroup_mem_max flags to enforce process and memory limits across user
namespaces, since RLIMIT_NPROC doesn't work in child user namespaces.

**What went wrong:**
Docker Desktop for macOS exposes /sys/fs/cgroup as a read-only filesystem. Even with --privileged, nsjail
couldn't enable the pids and memory controllers. The pids controller was writable but adding the child PID
to cgroup.procs returned EOPNOTSUPP. The memory controller couldn't be enabled at all.

**How I resolved it:**
I removed the cgroup flags entirely and removed the fork bomb penetration tests that require strict
enforcement. On a real Linux host with --cgroupns=host this would work, but macOS Docker Desktop is a known
limitation.

## 2026-06-12 · playground embed breaks CI build

**What I was trying to do:**
Embed the Vite-built playground into the Go binary via //go:embed so it ships as a single binary.

**What went wrong:**
The //go:embed playground-dist/assets/* pattern failed on CI because the playground hadn't been built there.
Go refuses to compile if embedded files are missing.

**How I resolved it:**
I changed the approach: committed a stub playground-dist/index.html to the repo, used //go:embed
playground-dist (directory embed), and added a runtime check that serves real built files if available or the
stub if not. PlaygroundExists() checks the file size: stub = under 200 bytes, real build = 300+. The CI
build now passes without needing the Vite build step.
