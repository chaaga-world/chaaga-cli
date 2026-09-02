package main

import "testing"

func TestIsValidSiblingFilename(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"style.css", true},
		{"app.js", true},
		{"icon.PNG", true},
		{"a", true},
		{"file-name_v2.min.js", true},
		{"", false},
		{"index.html", false}, // reserved — lives at the index route instead
		{"manifest", false},   // reserved — see app_server.dart's manifest route
		{"bad name.css", false},
		{"../escape.css", false},
		{"a/b.css", false},
		{`a\b.css`, false},
		{".hidden", false}, // must start with an alphanumeric
		{stringOfLength(128), true},
		{stringOfLength(129), false},
	}
	for _, c := range cases {
		if got := isValidSiblingFilename(c.name); got != c.want {
			t.Errorf("isValidSiblingFilename(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func stringOfLength(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
