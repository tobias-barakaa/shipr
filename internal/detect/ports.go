package detect

import (
	"fmt"
	"regexp"
	"strconv"

	"shipr/internal/app"
)

var (
	exposeRe     = regexp.MustCompile(`(?im)^\s*EXPOSE\s+(\d+)`)
	envPortRe    = regexp.MustCompile(`(?m)^\s*(?:export\s+)?PORT\s*=\s*["']?(\d+)`)
	scriptPortRe = regexp.MustCompile(`(?:^|\s)(?:--port|-p|PORT=)[ =]?(\d+)`)
)

// firstValidPort returns the first match that is a real TCP port (1-65535).
func firstValidPort(re *regexp.Regexp, text string) int {
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n >= 1 && n <= 65535 {
			return n
		}
	}
	return 0
}

func addPortFrom(fsys FS, a *app.Application, file string, re *regexp.Regexp, src app.PortSource, warnings *[]string) {
	p := join(a.Root, file)
	if !fsys.Has(p) {
		return
	}
	data, err := fsys.Read(p)
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("%s: reading %s: %v", displayDir(a.Root), file, err))
		return
	}
	if n := firstValidPort(re, string(data)); n != 0 {
		a.AddPort(n, src)
	}
}
