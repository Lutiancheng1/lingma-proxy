//go:build windows

package lingmaipc

const (
	PipeDir = `\\.\pipe\`
)

var PipePrefixes = []string{"lingma-", "qodercn-"}
