package environment

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Inputs struct {
	PATH            string
	Home            string
	CodexHome       string
	TempDir         string
	RunRoot         string
	CodexPath       string
	CorePath        string
	ProbeTimeout    time.Duration
	DispatchTimeout time.Duration
}

func Core(inputs Inputs) []string {
	values := map[string]string{
		"PATH":            inputs.PATH,
		"LANG":            "C",
		"LC_ALL":          "C",
		"TMPDIR":          inputs.TempDir,
		"ZEPHYR_RUN_ROOT": inputs.RunRoot,
	}
	return encode(values)
}

func Dispatcher(inputs Inputs) []string {
	if !filepath.IsAbs(inputs.CodexPath) || !filepath.IsAbs(inputs.CorePath) {
		return nil
	}
	probe, ok := wholePositiveSeconds(inputs.ProbeTimeout)
	if !ok {
		return nil
	}
	dispatch, ok := wholePositiveSeconds(inputs.DispatchTimeout)
	if !ok {
		return nil
	}
	values := map[string]string{
		"PATH":                          inputs.PATH,
		"HOME":                          inputs.Home,
		"LANG":                          "C",
		"LC_ALL":                        "C",
		"TMPDIR":                        inputs.TempDir,
		"ZEPHYR_CODEX_BIN":              filepath.Clean(inputs.CodexPath),
		"ZEPHYR_CORE_BIN":               filepath.Clean(inputs.CorePath),
		"ZEPHYR_RUN_ROOT":               inputs.RunRoot,
		"ZEPHYR_CODEX_PROBE_TIMEOUT":    strconv.FormatInt(probe, 10),
		"ZEPHYR_CODEX_DISPATCH_TIMEOUT": strconv.FormatInt(dispatch, 10),
	}
	if inputs.CodexHome != "" {
		values["CODEX_HOME"] = inputs.CodexHome
	}
	return encode(values)
}

func wholePositiveSeconds(value time.Duration) (int64, bool) {
	if value <= 0 || value%time.Second != 0 {
		return 0, false
	}
	return int64(value / time.Second), true
}

func encode(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if !validPart(key) || !validPart(value) || strings.Contains(key, "=") {
			return nil
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func validPart(value string) bool {
	return !strings.ContainsAny(value, "\x00\r\n")
}
