package config

import "testing"

func TestDefaultRegistryHasExpectedLanguages(t *testing.T) {
	expected := []string{"py3", "c", "cpp"}
	for _, id := range expected {
		if _, ok := DefaultRegistry[id]; !ok {
			t.Errorf("DefaultRegistry missing expected language: %s", id)
		}
	}
}

func TestDefaultRegistryNoExtras(t *testing.T) {
	if len(DefaultRegistry) != 3 {
		t.Errorf("DefaultRegistry has %d entries, want 3 (py3, c, cpp)", len(DefaultRegistry))
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
	if lc.DefaultLimits.WallTimeS <= 0 {
		t.Errorf("py3 WallTimeS = %d, want > 0", lc.DefaultLimits.WallTimeS)
	}
	if lc.DefaultLimits.MemoryKB <= 0 {
		t.Errorf("py3 MemoryKB = %d, want > 0", lc.DefaultLimits.MemoryKB)
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
	if len(lc.RunCmd) == 0 {
		t.Fatal("c RunCmd is empty")
	}
	if lc.RunCmd[0] != "./solution" {
		t.Errorf("c RunCmd[0] = %q, want ./solution", lc.RunCmd[0])
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
	if lc.ArtifactFilename != "solution" {
		t.Errorf("cpp ArtifactFilename = %q, want solution", lc.ArtifactFilename)
	}
	if len(lc.FlagAllowlist) == 0 {
		t.Error("cpp FlagAllowlist is empty, should have g++ flags")
	}
}

func TestFlagAllowlists(t *testing.T) {
	// Compiled languages should have allowlists
	for _, id := range []string{"c", "cpp"} {
		lc := DefaultRegistry[id]
		if len(lc.FlagAllowlist) == 0 {
			t.Errorf("%s has no flag allowlist", id)
			continue
		}
		// Should allow -O2
		found := false
		for _, f := range lc.FlagAllowlist {
			if f == "-O2" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s flag allowlist missing -O2", id)
		}
		// Should have wildcard pattern
		hasWildcard := false
		for _, f := range lc.FlagAllowlist {
			if len(f) > 0 && f[len(f)-1] == '*' {
				hasWildcard = true
				break
			}
		}
		if !hasWildcard {
			t.Errorf("%s flag allowlist has no wildcard patterns", id)
		}
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
