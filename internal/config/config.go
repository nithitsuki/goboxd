// Package config loads and exposes the language registry from a YAML
// configuration file. Each language has a source filename, optional build
// command, run command, and resource limits. LoadRegistry builds the
// registry at startup and swaps it atomically on SIGHUP reloads: readers
// always see a complete snapshot, never a half-built map.
//
// Template variables ({{source}}, {{artifact}}, {{flags}}) are expanded in
// command arguments at request time.
package config

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"gopkg.in/yaml.v3"
)

// Limits represents resource constraints for build or run stages.
type Limits struct {
	WallTimeS    int `yaml:"wall_time_s"`
	MemoryKB     int `yaml:"memory_kb"`
	MaxProcesses int `yaml:"max_processes"`
	CpuTimeS     int `yaml:"cpu_time_s"`
}

// StageCmd describes a build or run command with template variables.
// {{source}} and {{artifact}} are expanded per-request.
// {{flags}} is replaced with user-supplied flags at request time.
type StageCmd struct {
	Cmd    string   `yaml:"cmd"`
	Args   []string `yaml:"args"`
	Limits Limits   `yaml:"limits"`
	// Ceiling holds the measured-safe maxima for this stage. It is a Limits
	// value parallel to limits in the YAML (P1-10). When the ceiling key is
	// absent, HasCeiling is false and the stage limits act as the ceiling
	// (backward compatible). A zero ceiling field means that field's max
	// acts as the ceiling: a ceiling block may raise only some fields.
	Ceiling       Limits   `yaml:"ceiling"`
	HasCeiling    bool     `yaml:"-"`
	FlagAllowlist []string `yaml:"flag_allowlist,omitempty"`
}

// UnmarshalYAML decodes a stage and records whether the ceiling key was
// present. yaml.v3 cannot tell a missing key from a zero value, and an
// absent ceiling must fall back to the stage limits.
func (s *StageCmd) UnmarshalYAML(node *yaml.Node) error {
	type rawStage StageCmd
	var raw rawStage
	if err := node.Decode(&raw); err != nil {
		return err
	}
	*s = StageCmd(raw)
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "ceiling" {
			s.HasCeiling = true
			break
		}
	}
	return nil
}

// LanguageYAML is the per-language YAML config structure.
type LanguageYAML struct {
	ID             string    `yaml:"id"`
	Name           string    `yaml:"name"`
	SourceFilename string    `yaml:"source_filename"`
	Artifact       string    `yaml:"artifact,omitempty"`
	Build          *StageCmd `yaml:"build,omitempty"`
	Run            StageCmd  `yaml:"run"`
	SmokeCmd       string    `yaml:"smoke_cmd,omitempty"`
	SmokeArgs      []string  `yaml:"smoke_args,omitempty"`
	// SourceFilenameStrategy "fixed" means: always use the configured source
	// filename and ignore the client's (Java needs the class name to match).
	SourceFilenameStrategy string `yaml:"source_filename_strategy,omitempty"`
}

// ConfigYAML is the top-level YAML structure.
type ConfigYAML struct {
	Languages []LanguageYAML `yaml:"languages"`
}

// LanguageConfig holds the fully-resolved execution parameters for a language.
type LanguageConfig struct {
	ID                     string
	Name                   string
	SourceFilename         string
	ArtifactFilename       string
	BuildCmd               []string // pre-expanded build command + args (empty for interpreted)
	RunCmd                 []string // pre-expanded run command + args
	DefaultLimits          Limits   // merged limits (build limits for compiled, run for interpreted)
	BuildLimits            Limits   // YAML build limits (for compiled languages)
	RunLimits              Limits   // YAML run limits
	BuildCeiling           Limits   // measured-safe build maxima (build limits when ceiling absent)
	RunCeiling             Limits   // measured-safe run maxima (run limits when ceiling absent)
	DefaultCeiling         Limits   // merged ceiling (build for compiled, run for interpreted)
	FlagAllowlist          []string
	SmokeCmd               []string // readiness probe override (nil = probe build/run cmd)
	SourceFilenameStrategy string
}

// registry holds the current language registry as an immutable snapshot.
// LoadRegistry builds a fresh map and swaps it in with one atomic store;
// readers take the pointer once and see a complete map. The map is never
// mutated after Store.
var registry atomic.Pointer[map[string]LanguageConfig]

// Registry returns the current language registry. The returned map is a
// snapshot of the moment of the call: a concurrent reload swaps the whole
// map, so readers never see a partially updated registry. Do not modify the
// returned map; it is shared with other readers.
func Registry() map[string]LanguageConfig {
	if m := registry.Load(); m != nil {
		return *m
	}
	// LoadRegistry has not run yet (or failed at startup). Return an empty
	// map so callers never dereference nil.
	return map[string]LanguageConfig{}
}

// SetRegistryForTest replaces the registry snapshot. It exists only as a
// test seam: tests pin the registry to a deterministic set without touching
// the YAML file. Production code must use LoadRegistry. Do not call it from
// non-test code.
func SetRegistryForTest(m map[string]LanguageConfig) {
	registry.Store(&m)
}

// RegistryPath is the path to the YAML config file. Override for testing.
var RegistryPath = "config/languages.yml"

// LoadRegistry reads the YAML config and swaps the registry atomically.
// The new map is built and validated in full (parse, duplicate IDs,
// GOBOXD_LANGS/GOBOXD_EXCLUDE_LANGS filters) before the swap. On any error
// the function returns without touching the registry, so the previous
// registry stays active for every reader.
func LoadRegistry() error {
	data, err := os.ReadFile(RegistryPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", RegistryPath, err)
	}

	var cfg ConfigYAML
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parsing %s: %w", RegistryPath, err)
	}

	if len(cfg.Languages) == 0 {
		return fmt.Errorf("no languages defined in %s", RegistryPath)
	}

	newRegistry := make(map[string]LanguageConfig, len(cfg.Languages))
	for _, lang := range cfg.Languages {
		if lang.ID == "" {
			return fmt.Errorf("language with empty id in %s", RegistryPath)
		}

		lc := LanguageConfig{
			ID:                     lang.ID,
			Name:                   lang.Name,
			SourceFilename:         lang.SourceFilename,
			SourceFilenameStrategy: lang.SourceFilenameStrategy,
		}

		// Expand build command
		if lang.Build != nil {
			lc.BuildCmd = expandCmd(lang.Build.Cmd, lang.Build.Args, lang.SourceFilename, lang.Artifact)
			lc.ArtifactFilename = lang.Artifact
			lc.FlagAllowlist = lang.Build.FlagAllowlist
		}

		// Expand run command
		lc.RunCmd = expandCmd(lang.Run.Cmd, lang.Run.Args, lang.SourceFilename, lang.Artifact)

		// Readiness probe override (optional): languages whose build/run
		// binary cannot answer --version declare an explicit smoke command.
		if lang.SmokeCmd != "" {
			lc.SmokeCmd = append([]string{lang.SmokeCmd}, lang.SmokeArgs...)
		}

		// Store separate build and run limits from YAML. Ceilings default to
		// the stage limits when the ceiling key is absent (P1-10 backward
		// compatibility: the existing limits max acts as the ceiling).
		if lang.Build != nil {
			lc.BuildLimits = lang.Build.Limits
			lc.DefaultLimits = lang.Build.Limits
			lc.BuildCeiling = resolveCeiling(lang.Build.Ceiling, lang.Build.Limits, lang.Build.HasCeiling)
		}
		lc.RunLimits = lang.Run.Limits
		lc.RunCeiling = resolveCeiling(lang.Run.Ceiling, lang.Run.Limits, lang.Run.HasCeiling)
		if lang.Build == nil {
			lc.DefaultLimits = lang.Run.Limits
			lc.DefaultCeiling = lc.RunCeiling
		} else {
			lc.DefaultCeiling = lc.BuildCeiling
		}

		if _, exists := newRegistry[lang.ID]; exists {
			return fmt.Errorf("duplicate language id %q in %s", lang.ID, RegistryPath)
		}
		newRegistry[lang.ID] = lc
	}

	// GOBOXD_LANGS optionally restricts the registry to a comma-separated
	// subset (e.g. "py3,c,swift"). This lets an image built with a subset of
	// languages advertise only the languages that are actually installed.
	// Empty or "all" keeps every language.
	if filter := os.Getenv("GOBOXD_LANGS"); filter != "" && filter != "all" {
		keep := make(map[string]bool)
		for _, id := range strings.Split(filter, ",") {
			if id = strings.TrimSpace(id); id != "" {
				keep[id] = true
			}
		}
		for id := range newRegistry {
			if !keep[id] {
				delete(newRegistry, id)
			}
		}
	}

	// GOBOXD_EXCLUDE_LANGS removes languages from the registry (e.g. runtimes
	// whose virtual-memory reservation exceeds the strict RLIMIT_AS guard).
	// The image keeps every language; this only filters what the server
	// advertises and executes. Applied after GOBOXD_LANGS.
	if exclude := os.Getenv("GOBOXD_EXCLUDE_LANGS"); exclude != "" {
		for _, id := range strings.Split(exclude, ",") {
			if id = strings.TrimSpace(id); id != "" {
				delete(newRegistry, id)
			}
		}
	}

	registry.Store(&newRegistry)
	return nil
}

// resolveCeiling fills the ceiling fields that the YAML left unset with the
// stage max. A ceiling block may raise only some fields, and the resolved
// ceiling must hold a complete set of maxima. When the ceiling key is
// absent, the stage limits are the ceiling (backward compatible).
func resolveCeiling(ceiling, limits Limits, hasCeiling bool) Limits {
	if !hasCeiling {
		return limits
	}
	if ceiling.WallTimeS == 0 {
		ceiling.WallTimeS = limits.WallTimeS
	}
	if ceiling.MemoryKB == 0 {
		ceiling.MemoryKB = limits.MemoryKB
	}
	if ceiling.MaxProcesses == 0 {
		ceiling.MaxProcesses = limits.MaxProcesses
	}
	if ceiling.CpuTimeS == 0 {
		ceiling.CpuTimeS = limits.CpuTimeS
	}
	return ceiling
}

// expandCmd replaces {{source}} and {{artifact}} templates and builds the arg slice.
func expandCmd(cmd string, args []string, srcName, artifact string) []string {
	cmd = strings.ReplaceAll(cmd, "{{source}}", srcName)
	cmd = strings.ReplaceAll(cmd, "{{artifact}}", artifact)
	result := []string{cmd}
	for _, arg := range args {
		arg = strings.ReplaceAll(arg, "{{source}}", srcName)
		arg = strings.ReplaceAll(arg, "{{artifact}}", artifact)
		result = append(result, arg)
	}
	return result
}
