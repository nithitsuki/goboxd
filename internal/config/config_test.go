package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	// Load the YAML config from the project root
	pwd, _ := os.Getwd()
	// Walk up to find the project root (where config/languages.yml is)
	root := pwd
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(root, "config", "languages.yml")); err == nil {
			RegistryPath = filepath.Join(root, "config", "languages.yml")
			break
		}
		root = filepath.Dir(root)
	}

	if err := LoadRegistry(); err != nil {
		panic("failed to load YAML registry: " + err.Error())
	}
	os.Exit(m.Run())
}

func TestDefaultRegistryHasExpectedLanguages(t *testing.T) {
	expected := []string{"py3", "c", "cpp", "rust", "go"}
	for _, id := range expected {
		if _, ok := DefaultRegistry[id]; !ok {
			t.Errorf("DefaultRegistry missing expected language: %s", id)
		}
	}
}

func TestDefaultRegistryNoExtras(t *testing.T) {
	if len(DefaultRegistry) < 3 {
		t.Errorf("DefaultRegistry has only %d entries, expected at least 3", len(DefaultRegistry))
	}
}

func TestPy3Config(t *testing.T) {
	lc, ok := DefaultRegistry["py3"]
	if !ok {
		t.Fatal("py3 not in registry")
	}
	if lc.ID != "py3" {
		t.Errorf("py3 ID = %q, want py3", lc.ID)
	}
	if lc.Name == "" {
		t.Error("py3 Name is empty")
	}
	if len(lc.RunCmd) == 0 {
		t.Fatal("py3 RunCmd is empty")
	}
	if lc.RunCmd[0] != "/usr/bin/python3" {
		t.Errorf("py3 RunCmd[0] = %q, want /usr/bin/python3", lc.RunCmd[0])
	}
	if lc.SourceFilename == "" {
		t.Error("py3 SourceFilename is empty")
	}
}

func TestCConfig(t *testing.T) {
	lc, ok := DefaultRegistry["c"]
	if !ok {
		t.Fatal("c not in registry")
	}
	if lc.ID != "c" {
		t.Errorf("c ID = %q, want c", lc.ID)
	}
	if len(lc.BuildCmd) == 0 {
		t.Fatal("c BuildCmd is empty (compiled language)")
	}
	if lc.BuildCmd[0] != "/usr/bin/gcc" {
		t.Errorf("c BuildCmd[0] = %q, want /usr/bin/gcc", lc.BuildCmd[0])
	}
	if lc.ArtifactFilename != "solution" {
		t.Errorf("c ArtifactFilename = %q, want solution", lc.ArtifactFilename)
	}
	if len(lc.FlagAllowlist) == 0 {
		t.Error("c FlagAllowlist is empty, should have gcc flags")
	}
}

func TestCppConfig(t *testing.T) {
	lc, ok := DefaultRegistry["cpp"]
	if !ok {
		t.Fatal("cpp not in registry")
	}
	if lc.ID != "cpp" {
		t.Errorf("cpp ID = %q, want cpp", lc.ID)
	}
	if len(lc.BuildCmd) == 0 {
		t.Fatal("cpp BuildCmd is empty (compiled language)")
	}
	if lc.BuildCmd[0] != "/usr/bin/g++" {
		t.Errorf("cpp BuildCmd[0] = %q, want /usr/bin/g++", lc.BuildCmd[0])
	}
}

func TestInterpretedHasNoBuildCmd(t *testing.T) {
	lc := DefaultRegistry["py3"]
	if len(lc.BuildCmd) > 0 {
		t.Error("py3 should not have a BuildCmd (interpreted language)")
	}
}

func TestAllLimitsPositive(t *testing.T) {
	for id, lc := range DefaultRegistry {
		if lc.DefaultLimits.WallTimeS <= 0 {
			t.Errorf("%s WallTimeS = %d, want positive", id, lc.DefaultLimits.WallTimeS)
		}
		if lc.DefaultLimits.MemoryKB <= 0 {
			t.Errorf("%s MemoryKB = %d, want positive", id, lc.DefaultLimits.MemoryKB)
		}
		if lc.DefaultLimits.MaxProcesses <= 0 {
			t.Errorf("%s MaxProcesses = %d, want positive", id, lc.DefaultLimits.MaxProcesses)
		}
	}
}

func TestLoadRegistryFiltersByEnv(t *testing.T) {
	t.Setenv("GOBOXD_LANGS", "py3,c,doesnotexist")
	if err := LoadRegistry(); err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	for _, id := range []string{"py3", "c"} {
		if _, ok := DefaultRegistry[id]; !ok {
			t.Errorf("GOBOXD_LANGS=py3,c: expected %q in registry", id)
		}
	}
	if len(DefaultRegistry) != 2 {
		t.Errorf("GOBOXD_LANGS=py3,c: got %d languages, want 2", len(DefaultRegistry))
	}
}

func TestLoadRegistryAllWhenEnvUnset(t *testing.T) {
	t.Setenv("GOBOXD_LANGS", "")
	if err := LoadRegistry(); err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if len(DefaultRegistry) < 28 {
		t.Errorf("no filter: got %d languages, want >= 28", len(DefaultRegistry))
	}
}

func TestLoadRegistryAllKeyword(t *testing.T) {
	t.Setenv("GOBOXD_LANGS", "all")
	if err := LoadRegistry(); err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if len(DefaultRegistry) < 28 {
		t.Errorf("GOBOXD_LANGS=all: got %d languages, want >= 28", len(DefaultRegistry))
	}
}

func TestLoadRegistryExcludesByEnv(t *testing.T) {
	t.Setenv("GOBOXD_LANGS", "")
	t.Setenv("GOBOXD_EXCLUDE_LANGS", "csharp,elixir")
	if err := LoadRegistry(); err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if _, ok := DefaultRegistry["csharp"]; ok {
		t.Error("GOBOXD_EXCLUDE_LANGS=csharp,elixir: csharp should be excluded")
	}
	if _, ok := DefaultRegistry["elixir"]; ok {
		t.Error("GOBOXD_EXCLUDE_LANGS=csharp,elixir: elixir should be excluded")
	}
	if _, ok := DefaultRegistry["py3"]; !ok {
		t.Error("GOBOXD_EXCLUDE_LANGS must not remove py3")
	}
}

// TestSmokeCmdParsedFromYAML locks the per-language readiness probe override:
// a language whose build/run binary cannot answer --version can declare an
// explicit smoke command in the YAML.
func TestSmokeCmdParsedFromYAML(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "languages.yml")
	yaml := `languages:
  - id: fake
    name: Fake
    source_filename: fake.fk
    run:
      cmd: /bin/false
      args: ["{{source}}"]
      limits: {wall_time_s: 1, memory_kb: 1024, max_processes: 1}
    smoke_cmd: /bin/echo
    smoke_args: ["fake-version"]
`
	if err := os.WriteFile(tmp, []byte(yaml), 0644); err != nil {
		t.Fatalf("writing yaml: %v", err)
	}
	orig := RegistryPath
	RegistryPath = tmp
	defer func() { RegistryPath = orig }()
	if err := LoadRegistry(); err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	lc, ok := DefaultRegistry["fake"]
	if !ok {
		t.Fatal("fake language not loaded")
	}
	if len(lc.SmokeCmd) != 2 || lc.SmokeCmd[0] != "/bin/echo" || lc.SmokeCmd[1] != "fake-version" {
		t.Errorf("SmokeCmd = %v, want [/bin/echo fake-version]", lc.SmokeCmd)
	}
}

// TestJavaFilenameStrategyParsed locks the fixed source-filename strategy for
// Java: javac needs the public class name to match the file name, so the
// registry pins Main.java regardless of the client's filename.
func TestJavaFilenameStrategyParsed(t *testing.T) {
	lc, ok := DefaultRegistry["java"]
	if !ok {
		t.Skip("java not in registry")
	}
	if lc.SourceFilenameStrategy != "fixed" {
		t.Errorf("java SourceFilenameStrategy = %q, want fixed", lc.SourceFilenameStrategy)
	}
}
