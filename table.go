package go_partman

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

type TableName struct {
	SchemaName string
	ParentName string
	Bounds     Bounds
	TenantId   string // optional
	IsDefault  bool
	Interval   time.Time // optional: pg_partman supports different intervals
}

func alphaNumericRegex() (*regexp.Regexp, error) {
	re, err := regexp.Compile(`^(\w)+$`)
	if err != nil {
		return nil, err
	}
	return re, nil
}

// Build returns the fully qualified table name
// test.user_logs_TENANT1_20240101
// test.user_logs_TENANT1_default
//
// test.user_logs_20240101
// test.user_logs_default
func (tn TableName) Build() (string, error) {
	builder := strings.Builder{}
	re, err := alphaNumericRegex()
	if err != nil {
		return "", err
	}

	sMatches := re.FindStringSubmatch(tn.SchemaName)
	if len(sMatches) == 0 {
		return "", fmt.Errorf("schema name contains invalid characters")
	}
	builder.WriteString(tn.SchemaName)
	builder.WriteString(".")

	pMatches := re.FindStringSubmatch(tn.ParentName)
	if len(pMatches) == 0 {
		return "", fmt.Errorf("parent name contains invalid characters")
	}

	builder.WriteString(tn.ParentName)

	if len(tn.TenantId) > 0 {
		// Find the match
		matches := re.FindStringSubmatch(tn.TenantId)
		if len(matches) == 0 {
			return "", fmt.Errorf("tenant id contains invalid characters")
		}

		builder.WriteString("_")
		builder.WriteString(strings.ToUpper(tn.TenantId)) // to ensure the names are consistent
	}

	if tn.IsDefault {
		builder.WriteString("_default")
	} else {
		builder.WriteString("_")
		builder.WriteString(tn.Bounds.From.Format(DateNoHyphens))
	}

	return builder.String(), nil
}
