package environment_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signaturekey/zephyr/internal/codexharness/environment"
	harnessprocess "github.com/signaturekey/zephyr/internal/codexharness/process"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCoreReturnsSortedClosedEnvironment(t *testing.T) {
	inputs := environment.Inputs{
		PATH:    "/usr/bin:/bin",
		TempDir: "/private/tmp/zephyr",
		RunRoot: "/private/cache/zephyr/runs",
	}

	assert.Equal(t, []string{
		"LANG=C",
		"LC_ALL=C",
		"PATH=/usr/bin:/bin",
		"TMPDIR=/private/tmp/zephyr",
		"ZEPHYR_RUN_ROOT=/private/cache/zephyr/runs",
	}, environment.Core(inputs))
}

func TestDispatcherAddsOnlyPrivateHomesAbsoluteBinariesAndFixedTimeouts(t *testing.T) {
	inputs := environment.Inputs{
		PATH:            "/usr/bin:/bin",
		Home:            "/private/op/home",
		CodexHome:       "/private/op/codex-home",
		TempDir:         "/private/op/tmp",
		RunRoot:         "/private/op/runs",
		CodexPath:       "/opt/codex/bin/codex",
		CorePath:        "/opt/zephyr/bin/zephyr",
		ProbeTimeout:    11 * time.Second,
		DispatchTimeout: 901 * time.Second,
	}

	assert.Equal(t, []string{
		"CODEX_HOME=/private/op/codex-home",
		"HOME=/private/op/home",
		"LANG=C",
		"LC_ALL=C",
		"PATH=/usr/bin:/bin",
		"TMPDIR=/private/op/tmp",
		"ZEPHYR_CODEX_BIN=/opt/codex/bin/codex",
		"ZEPHYR_CODEX_DISPATCH_TIMEOUT=901",
		"ZEPHYR_CODEX_PROBE_TIMEOUT=11",
		"ZEPHYR_CORE_BIN=/opt/zephyr/bin/zephyr",
		"ZEPHYR_RUN_ROOT=/private/op/runs",
	}, environment.Dispatcher(inputs))
}

func TestDispatcherOmitsEmptyOptionalCodexHome(t *testing.T) {
	inputs := validInputs(t)
	inputs.CodexHome = ""

	for _, entry := range environment.Dispatcher(inputs) {
		assert.False(t, strings.HasPrefix(entry, "CODEX_HOME="))
	}
}

func TestEnvironmentRejectsInjectionRelativeBinariesAndDuplicateKeys(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*environment.Inputs)
	}{
		{name: "newline", mutate: func(inputs *environment.Inputs) { inputs.Home = "/safe\nINJECTED=yes" }},
		{name: "nul", mutate: func(inputs *environment.Inputs) { inputs.RunRoot = "/safe\x00bad" }},
		{name: "relative codex", mutate: func(inputs *environment.Inputs) { inputs.CodexPath = "bin/codex" }},
		{name: "relative core", mutate: func(inputs *environment.Inputs) { inputs.CorePath = "zephyr" }},
		{name: "duplicate key injection", mutate: func(inputs *environment.Inputs) { inputs.PATH = "/bin\nPATH=/evil" }},
		{name: "fractional timeout", mutate: func(inputs *environment.Inputs) { inputs.ProbeTimeout = 1500 * time.Millisecond }},
		{name: "zero timeout", mutate: func(inputs *environment.Inputs) { inputs.DispatchTimeout = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inputs := validInputs(t)
			test.mutate(&inputs)
			assert.Nil(t, environment.Dispatcher(inputs))
		})
	}
}

func TestGeneratedEnvironmentsDoNotLeakParentSecretsToChild(t *testing.T) {
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws-parent-secret")
	t.Setenv("OPENAI_API_KEY", "openai-parent-secret")
	t.Setenv("CI_JOB_TOKEN", "ci-parent-secret")
	inputs := validInputs(t)

	for name, childEnvironment := range map[string][]string{
		"fake core":        environment.Core(inputs),
		"dispatcher":       environment.Dispatcher(inputs),
		"fake codex child": environment.Dispatcher(inputs),
	} {
		t.Run(name, func(t *testing.T) {
			request := harnessprocess.Request{
				Path: os.Args[0],
				Args: []string{"-test.run=^TestEnvironmentChild$"},
				Env:  append(childEnvironment, "ZEPHYR_ENV_HELPER=1"),
			}
			result, err := (harnessprocess.ExecRunner{}).Run(context.Background(), request)
			require.NoError(t, err)
			assert.Equal(t, 0, result.ExitCode)
			assert.Empty(t, result.Stdout)
			assert.Empty(t, result.Stderr)
		})
	}
}

func TestEnvironmentChild(t *testing.T) {
	if os.Getenv("ZEPHYR_ENV_HELPER") != "1" {
		return
	}
	for _, key := range []string{"AWS_SECRET_ACCESS_KEY", "OPENAI_API_KEY", "CI_JOB_TOKEN"} {
		if value := os.Getenv(key); value != "" {
			_, _ = os.Stderr.WriteString(key + " leaked")
			os.Exit(99)
		}
	}
	os.Exit(0)
}

func validInputs(t *testing.T) environment.Inputs {
	t.Helper()
	root := t.TempDir()
	return environment.Inputs{
		PATH:            "/usr/bin:/bin",
		Home:            filepath.Join(root, "home"),
		CodexHome:       filepath.Join(root, "codex-home"),
		TempDir:         filepath.Join(root, "tmp"),
		RunRoot:         filepath.Join(root, "runs"),
		CodexPath:       "/opt/codex/bin/codex",
		CorePath:        "/opt/zephyr/bin/zephyr",
		ProbeTimeout:    10 * time.Second,
		DispatchTimeout: 900 * time.Second,
	}
}
