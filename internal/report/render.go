package report

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/signaturekey/zephyr/internal/schema"
)

type RenderOptions struct {
	IncludeP3        bool
	MaxFinalFindings int
}

type renderView struct {
	Review       Review
	P0           []FinalFinding
	P1           []FinalFinding
	P2           []FinalFinding
	P3           []FinalFinding
	Omitted      map[string]int
	ShownCount   int
	FindingCount int
	Sections     []renderSection
}

type renderSection struct {
	Severity string
	Items    []FinalFinding
}

func RenderMarkdown(review Review, options RenderOptions) ([]byte, error) {
	if review.Version != Version || review.RunID == "" {
		return nil, fmt.Errorf("cannot render invalid review")
	}
	if options.MaxFinalFindings <= 0 {
		options.MaxFinalFindings = 8
	}
	view := buildView(review, options)
	parsed, err := template.New("review").Funcs(template.FuncMap{
		"join":              func(values []string) string { return strings.Join(values, ", ") },
		"location":          findingLocation,
		"candidateLocation": candidateLocation,
		"reasonSummary":     reasonSummary,
		"clean":             cleanMarkdown,
		"code":              cleanCodeSpan,
		"timestamp": func(value time.Time) string {
			if value.IsZero() {
				return ""
			}
			return value.UTC().Format(time.RFC3339)
		},
		"hasTimestamp": func(value time.Time) bool { return !value.IsZero() },
	}).Parse(markdownTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse report template: %w", err)
	}
	var output bytes.Buffer
	if err := parsed.Execute(&output, view); err != nil {
		return nil, fmt.Errorf("render report: %w", err)
	}
	return output.Bytes(), nil
}

func buildView(review Review, options RenderOptions) renderView {
	bySeverity := map[schema.Severity][]FinalFinding{}
	for _, finding := range review.Findings {
		bySeverity[finding.Candidate.Severity] = append(bySeverity[finding.Candidate.Severity], finding)
	}
	for severity := range bySeverity {
		sortFinal(bySeverity[severity])
	}
	view := renderView{
		Review:       review,
		Omitted:      map[string]int{},
		FindingCount: len(review.Findings),
	}
	view.P0 = append([]FinalFinding(nil), bySeverity[schema.SeverityP0]...)
	remaining := options.MaxFinalFindings - len(view.P0)
	if remaining < 0 {
		remaining = 0
	}
	view.P1, view.Omitted["P1"] = take(bySeverity[schema.SeverityP1], min(5, remaining))
	remaining -= len(view.P1)
	view.P2, view.Omitted["P2"] = take(bySeverity[schema.SeverityP2], min(3, max(remaining, 0)))
	remaining -= len(view.P2)
	showP3 := options.IncludeP3 || len(view.P0)+len(view.P1)+len(view.P2) == 0
	if showP3 {
		view.P3, view.Omitted["P3"] = take(bySeverity[schema.SeverityP3], max(remaining, 0))
	} else {
		view.Omitted["P3"] = len(bySeverity[schema.SeverityP3])
	}
	view.ShownCount = len(view.P0) + len(view.P1) + len(view.P2) + len(view.P3)
	view.Sections = []renderSection{
		{Severity: "P0", Items: view.P0},
		{Severity: "P1", Items: view.P1},
		{Severity: "P2", Items: view.P2},
		{Severity: "P3", Items: view.P3},
	}
	return view
}

func take(values []FinalFinding, count int) ([]FinalFinding, int) {
	if count < 0 {
		count = 0
	}
	if count >= len(values) {
		return append([]FinalFinding(nil), values...), 0
	}
	return append([]FinalFinding(nil), values[:count]...), len(values) - count
}

func findingLocation(finding FinalFinding) string {
	return candidateLocation(finding.Candidate)
}

func candidateLocation(candidate schema.CandidateFinding) string {
	location := candidate.Location
	if location.IsCode() {
		return lineLocation(location.File, location.LineStart, location.LineEnd)
	}
	value := location.Artifact
	if location.Section != "" {
		value += " — " + location.Section
	}
	if location.LineStart > 0 {
		value = lineLocation(value, location.LineStart, location.LineEnd)
	}
	return cleanCodeSpan(value)
}

func lineLocation(path string, start, end int) string {
	if start <= 0 {
		return cleanCodeSpan(path)
	}
	if end <= 0 || end == start {
		return cleanCodeSpan(fmt.Sprintf("%s:%d", path, start))
	}
	return cleanCodeSpan(fmt.Sprintf("%s:%d-%d", path, start, end))
}

func reasonSummary(decision RoleDecision) string {
	if len(decision.Reasons) == 0 {
		return "причина не зафиксирована"
	}
	return strings.Join(decision.Reasons, "; ")
}

func cleanMarkdown(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 2000 {
		value = value[:2000] + "…"
	}
	replacer := strings.NewReplacer(
		`\`, `\\`, "`", "'", "*", `\*`, "_", `\_`, "{", `\{`, "}", `\}`,
		"[", `\[`, "]", `\]`, "<", `\<`, ">", `\>`, "(", `\(`, ")", `\)`,
		"#", `\#`, "+", `\+`, "-", `\-`, "!", `\!`, "|", `\|`,
	)
	value = replacer.Replace(value)
	return value
}

func cleanCodeSpan(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.ReplaceAll(value, "`", "'")
	if len(value) > 2000 {
		value = value[:2000] + "…"
	}
	return value
}

const markdownTemplate = `# Ревью Zephyr

## Область проверки

- Статус: {{clean .Review.Status}}
- Режим: {{clean .Review.Scope.Mode}}
- Источник: {{clean .Review.Scope.Source}}
- Репозиторий: {{clean .Review.Scope.Repository}}
{{- if .Review.Scope.Branch}}
- Ветка: {{clean .Review.Scope.Branch}}
{{- end}}
{{- if .Review.Scope.Head}}
- HEAD checkout: {{clean .Review.Scope.Head}}
{{- end}}
{{- if .Review.Scope.BaseRef}}
- Базовая ссылка: {{clean .Review.Scope.BaseRef}}
{{- end}}
{{- if .Review.Scope.BaseSHA}}
- Базовый SHA: {{clean .Review.Scope.BaseSHA}}
{{- end}}
{{- if .Review.Scope.TargetSHA}}
- Проверенный SHA: {{clean .Review.Scope.TargetSHA}}
{{- end}}
{{- if .Review.Scope.CommitRange}}
- Диапазон коммитов: {{clean .Review.Scope.CommitRange}}
{{- end}}
{{- if .Review.Scope.Plan}}
- План: {{clean .Review.Scope.Plan}}
{{- end}}
{{- if .Review.Scope.PlanHash}}
- Хеш плана: {{clean .Review.Scope.PlanHash}}
{{- end}}
{{- if .Review.Scope.Stale}}
- Снимок устарел: отчёт относится к исходному SHA и отпечатку рабочего дерева
{{- end}}

## Источники контекста
{{- range .Review.Scope.Sources}}

- {{clean .Source}}{{if .Key}} — {{clean .Key}}{{end}}{{if .URL}} — {{clean .URL}}{{end}} — {{clean .ContentHash}}{{if hasTimestamp .FetchedAt}} — {{timestamp .FetchedAt}}{{end}}
{{- else}}

- не зафиксированы
{{- end}}

## Маршрутизация

Выбранные роли:
{{- range .Review.Routing.Selected}}
- ` + "`{{code .Role}}`" + ` — {{clean (reasonSummary .)}}
{{- else}}
- нет
{{- end}}

Исключённые роли:
{{- range .Review.Routing.Excluded}}
- ` + "`{{code .Role}}`" + ` — {{clean (reasonSummary .)}}
{{- else}}
- нет
{{- end}}

## Подтверждённые замечания
{{- if eq .FindingCount 0}}

Доказуемых проблем в проверенной области не найдено.
{{- end}}
{{- range .Sections}}
{{- if .Items}}

### {{.Severity}}
{{- range .Items}}

#### [{{.Candidate.Severity}}] {{clean .Candidate.Title}}

- Место: ` + "`{{location .}}`" + `
- Роли: {{clean (join .SourceRoles)}}
- Влияние: {{clean .Candidate.Impact}}
- Доказательства: {{clean .Candidate.Evidence.ExecutionPath}}; {{clean .Candidate.Evidence.ViolatedInvariant}}
- Проверенный контрпример: {{clean .Candidate.Evidence.FalsifierChecked}}
- Рекомендация: {{clean .Candidate.Recommendation}}
- Вердикт gate: {{clean .GateReason}}
{{- end}}
{{- end}}
{{- end}}
{{- if or (index .Omitted "P1") (index .Omitted "P2") (index .Omitted "P3")}}

Дополнительные подтверждённые замечания скрыты из краткого Markdown-отчёта: P1={{index .Omitted "P1"}}, P2={{index .Omitted "P2"}}, P3={{index .Omitted "P3"}}. Полный список — в ` + "`review.json`" + `.
{{- end}}

## Требуется решение человека
{{- range .Review.NeedsHuman}}

- **{{clean .Candidate.Title}}** (` + "`{{candidateLocation .Candidate}}`" + `): {{clean .Reason}}
{{- else}}

- нет
{{- end}}

## Ограничения покрытия
{{- range .Review.CoverageLimits}}

- {{clean .}}
{{- else}}

- не зафиксированы
{{- end}}

## Отклонённые кандидаты

- Количество: {{.Review.Rejected.Count}}
- Полный артефакт: ` + "`{{code .Review.Rejected.Path}}`" + `
{{- range $reason, $count := .Review.Rejected.ByReason}}
- ` + "`{{code $reason}}`" + `: {{$count}}
{{- end}}
`
