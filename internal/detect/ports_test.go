package detect

import (
	"testing"

	"shipr/internal/app"
)

func TestPortPrecedence(t *testing.T) {
	scriptFlag := `{"name":"x","scripts":{"dev":"astro dev --port 3000"},"dependencies":{"astro":"4"}}`
	scriptEnv := `{"name":"x","scripts":{"start":"PORT=8081 node server.js"},"dependencies":{"express":"4"}}`

	cases := []struct {
		name  string
		files map[string]string
		want  int
		src   app.PortSource
	}{
		{"framework default", map[string]string{"package.json": astroPkg}, 4321, app.PortDefault},
		{"script flag", map[string]string{"package.json": scriptFlag}, 3000, app.PortScript},
		{"script env", map[string]string{"package.json": scriptEnv}, 8081, app.PortScript},
		{".env", map[string]string{"package.json": astroPkg, ".env": "PORT=9000\n"}, 9000, app.PortEnv},
		{".env export and quotes", map[string]string{"package.json": astroPkg, ".env": "export PORT=\"9001\"\n"}, 9001, app.PortEnv},
		{".env.example", map[string]string{"package.json": astroPkg, ".env.example": "PORT=9100\n"}, 9100, app.PortEnvExample},
		{".env beats .env.example", map[string]string{"package.json": astroPkg, ".env": "PORT=9000", ".env.example": "PORT=9100"}, 9000, app.PortEnv},
		{"script beats .env", map[string]string{"package.json": scriptFlag, ".env": "PORT=9000"}, 3000, app.PortScript},
		{"Dockerfile EXPOSE", map[string]string{"package.json": astroPkg, "Dockerfile": "FROM node\nEXPOSE 7000/tcp\n"}, 7000, app.PortDockerfile},
		{"Dockerfile beats everything", map[string]string{
			"package.json": scriptFlag, ".env": "PORT=9000", "Dockerfile": "EXPOSE 7000",
		}, 7000, app.PortDockerfile},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := single(t, c.files)
			p, ok := a.Port()
			if !ok || p.Number != c.want || p.Source != c.src {
				t.Errorf("port = %+v (ok=%v), want %d from %s", p, ok, c.want, c.src)
			}
		})
	}
}

func TestInvalidPortsAreIgnored(t *testing.T) {
	badScript := `{"scripts":{"dev":"astro dev --port 99999"},"dependencies":{"astro":"4"}}`
	cases := map[string]map[string]string{
		"env too large":    {"package.json": astroPkg, ".env": "PORT=99999\n"},
		"env zero":         {"package.json": astroPkg, ".env": "PORT=0\n"},
		"env not a number": {"package.json": astroPkg, ".env": "PORT=abc\n"},
		"dockerfile large": {"package.json": astroPkg, "Dockerfile": "EXPOSE 70000\n"},
		"dockerfile huge":  {"package.json": astroPkg, "Dockerfile": "EXPOSE 123456789012345678901\n"},
		"script too large": {"package.json": badScript},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			a := single(t, files)
			p, _ := a.Port()
			if p.Number != 4321 || p.Source != app.PortDefault || len(a.Ports) != 1 {
				t.Errorf("want only the 4321 default, got %+v", a.Ports)
			}
		})
	}
}

func TestConflictingPortsArePreserved(t *testing.T) {
	scriptFlag := `{"scripts":{"dev":"astro dev --port 3000"},"dependencies":{"astro":"4"}}`
	a := single(t, map[string]string{
		"package.json": scriptFlag,
		".env":         "PORT=9000\n",
		"Dockerfile":   "EXPOSE 7000\n",
	})

	want := []app.PortCandidate{
		{Number: 7000, Source: app.PortDockerfile},
		{Number: 3000, Source: app.PortScript},
		{Number: 9000, Source: app.PortEnv},
	}
	if len(a.Ports) != len(want) {
		t.Fatalf("ports = %+v, want %+v", a.Ports, want)
	}
	for i := range want {
		if a.Ports[i] != want[i] {
			t.Errorf("Ports[%d] = %+v, want %+v", i, a.Ports[i], want[i])
		}
	}
	if got := len(a.PortConflicts()); got != 2 {
		t.Errorf("want 2 conflicts, got %d", got)
	}
}

func TestAgreeingPortsAreNotConflicts(t *testing.T) {
	a := single(t, map[string]string{
		"package.json": astroPkg,
		".env":         "PORT=7000\n",
		"Dockerfile":   "EXPOSE 7000\n",
	})
	if got := len(a.PortConflicts()); got != 0 {
		t.Errorf("want no conflicts, got %v", a.PortConflicts())
	}
}
