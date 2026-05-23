package config

// Limits represents the resource constraints for build or run stages.
type Limits struct {
	WallTimeS    int `yaml:"wall_time_s"`
	MemoryKB     int `yaml:"memory_kb"`
	MaxProcesses int `yaml:"max_processes"`
}

// LanguageConfig holds the execution parameters for a programming language.
type LanguageConfig struct {
	ID               string   `yaml:"id"`
	Name             string   `yaml:"name"`
	Version          string   `yaml:"version"`
	BuildCmd         []string `yaml:"build_cmd"`
	RunCmd           []string `yaml:"run_cmd"`
	SourceFilename   string   `yaml:"source_filename"`
	ArtifactFilename string   `yaml:"artifact_filename"`
	DefaultLimits    Limits   `yaml:"default_run_limits"`
}

// Stub for hardcoded py3 config until we implement the YAML registry
var Py3Stub = LanguageConfig{
	ID:               "py3",
	Name:             "Python 3",
	Version:          "Python 3.11", // Exact version will be probed later
	BuildCmd:         nil,
	RunCmd:           []string{"/usr/bin/python3", "main.py"},
	SourceFilename:   "main.py",
	ArtifactFilename: "",
	DefaultLimits: Limits{
		WallTimeS:    9,
		MemoryKB:     102400, // 100 MiB
		MaxProcesses: 100,
	},
}
