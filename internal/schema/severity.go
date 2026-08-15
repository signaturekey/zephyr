package schema

type Severity string

const (
	SeverityP0 Severity = "P0"
	SeverityP1 Severity = "P1"
	SeverityP2 Severity = "P2"
	SeverityP3 Severity = "P3"
)

func (severity Severity) Valid() bool {
	switch severity {
	case SeverityP0, SeverityP1, SeverityP2, SeverityP3:
		return true
	default:
		return false
	}
}

func (severity Severity) Rank() int {
	switch severity {
	case SeverityP0:
		return 0
	case SeverityP1:
		return 1
	case SeverityP2:
		return 2
	case SeverityP3:
		return 3
	default:
		return 4
	}
}
