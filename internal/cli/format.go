package cli

import (
	"fmt"
	"strings"
	"time"

	"mem-x/internal/resp"
)

// FormatReply renders a resp.Reply as a redis-cli-style string for display.
func FormatReply(r resp.Reply) string {
	switch r.Kind {
	case resp.Simple:
		return string(r.Str)
	case resp.RError:
		return "(error) " + string(r.Str)
	case resp.RInteger:
		return fmt.Sprintf("(integer) %d", r.Int)
	case resp.Bulk:
		return fmt.Sprintf("%q", string(r.Str))
	case resp.NullBulk:
		return "(nil)"
	case resp.RArray:
		if len(r.Array) == 0 {
			return "(empty array)"
		}
		var b strings.Builder
		for i, el := range r.Array {
			fmt.Fprintf(&b, "%d) %s", i+1, FormatReply(el))
			if i+1 < len(r.Array) {
				b.WriteString("\n")
			}
		}
		return b.String()
	default:
		return "(unknown)"
	}
}

// FormatLatency returns a short ms string like "(0.42 ms)".
func FormatLatency(d time.Duration) string {
	ms := float64(d.Microseconds()) / 1000
	return fmt.Sprintf("(%.2f ms)", ms)
}
