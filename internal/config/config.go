package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Limits represents resource constraints for build or run stages.
type Limits struct {
	WallTimeS    int `yaml:"wall_time_s"`
	MemoryKB     int `yaml:"memory_kb"`
	MaxProcesses int `yaml:"max_processes"`
}

// StageCmd describes a build or run command with template variables.
// {{source}} and {{artifact}} are expanded per-request.
// {{flags}} is replaced with user-supplied flags at request time.
type StageCmd struct {
	Cmd            string   `yaml:"cmd"`
	Args           []string `yaml:"args"`
	Limits         Limits   `yaml:"limits"`
	FlagAllowlist  []string `yaml:"flag_allowlist,omitempty"`
}

// LanguageYAML is the per-language YAML config structure.
type LanguageYAML struct {
	ID               string    `yaml:"id"`
	Name             string    `yaml:"name"`
	SourceFilename   string    `yaml:"source_filename"`
	Artifact         string    `yaml:"artifact,omitempty"`
	Build            *StageCmd `yaml:"build,omitempty"`
	Run              StageCmd  `yaml:"run"`
}

// ConfigYAML is the top-level YAML structure.
type ConfigYAML struct {
	Languages []LanguageYAML `yaml:"languages"`
}

// LanguageConfig holds the fully-resolved execution parameters for a language.
type LanguageConfig struct {
	ID               string
	Name             string
	SourceFilename   string
	ArtifactFilename string
	BuildCmd         []string // pre-expanded build command + args (empty for interpreted)
	RunCmd           []string // pre-expanded run command + args
	DefaultLimits    Limits
	FlagAllowlist    []string
}

// DefaultRegistry is populated at startup from YAML, with hardcoded fallback.
var DefaultRegistry = map[string]LanguageConfig{}

// RegistryPath is the path to the YAML config file. Override for testing.
var RegistryPath = "config/languages.yml"

// LoadRegistry reads the YAML config and populates DefaultRegistry.
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

	DefaultRegistry = make(map[string]LanguageConfig, len(cfg.Languages))
	for _, lang := range cfg.Languages {
		if lang.ID == "" {
			return fmt.Errorf("language with empty id in %s", RegistryPath)
		}

		lc := LanguageConfig{
			ID:             lang.ID,
			Name:           lang.Name,
			SourceFilename: lang.SourceFilename,
		}

		// Expand build command
		if lang.Build != nil {
			lc.BuildCmd = expandCmd(lang.Build.Cmd, lang.Build.Args, lang.SourceFilename, lang.Artifact)
			lc.ArtifactFilename = lang.Artifact
			lc.FlagAllowlist = lang.Build.FlagAllowlist
		}

		// Expand run command
		lc.RunCmd = expandCmd(lang.Run.Cmd, lang.Run.Args, lang.SourceFilename, lang.Artifact)

		// Merge limits: build limits take priority for build, run limits for run
		if lang.Build != nil {
			lc.DefaultLimits = lang.Build.Limits
		} else {
			lc.DefaultLimits = lang.Run.Limits
		}

		if _, exists := DefaultRegistry[lang.ID]; exists {
			return fmt.Errorf("duplicate language id %q in %s", lang.ID, RegistryPath)
		}
		DefaultRegistry[lang.ID] = lc
	}

	return nil
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
