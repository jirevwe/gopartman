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
	Interval   time.Time // optional, pg_partman supports different intervals
}

func (tn TableName) Build() (string, error) {
	// test.user_logs_TENANT1_20240101
	// test.user_logs_TENANT1_default

	// test.user_logs_20240101
	// test.user_logs_default

	builder := strings.Builder{}
	builder.WriteString(tn.SchemaName)
	builder.WriteString(".")
	builder.WriteString(tn.ParentName)

	if len(tn.TenantId) > 0 {
		re, err := regexp.Compile(`^(\w)+$`)
		if err != nil {
			return "", err
		}

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
