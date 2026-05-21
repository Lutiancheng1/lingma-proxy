//go:build !windows

package lingmaipc

const (
	PipeDir = ""
)

var PipePrefixes = []string{"lingma-", "qodercn-"}
