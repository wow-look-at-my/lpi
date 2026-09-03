package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRouteArgs(t *testing.T) {
	t.Serial()
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, nil},
		{"help flag", []string{"--help"}, []string{"--help"}},
		{"version flag", []string{"--version"}, []string{"--version"}},
		{"short flag", []string{"-x"}, []string{"-x"}},
		{"bare command with flags", []string{"make", "-j8", "target"},
			[]string{"auto", "--", "make", "-j8", "target"}},
		{"explicit dashes", []string{"--", "make"}, []string{"auto", "--", "make"}},
		{"explicit dashes shadowed name", []string{"--", "run", "-x"},
			[]string{"auto", "--", "run", "-x"}},
		{"run unchanged", []string{"run", "--", "/bin/true"}, []string{"run", "--", "/bin/true"}},
		{"auto unchanged", []string{"auto", "--", "x"}, []string{"auto", "--", "x"}},
		{"analyze unchanged", []string{"analyze", "x.log"}, []string{"analyze", "x.log"}},
		{"model unchanged", []string{"model", "list"}, []string{"model", "list"}},
		{"help unchanged", []string{"help", "run"}, []string{"help", "run"}},
		{"completion unchanged", []string{"completion", "bash"}, []string{"completion", "bash"}},
		{"__complete unchanged", []string{"__complete", "r"}, []string{"__complete", "r"}},
		{"__completeNoDesc unchanged", []string{"__completeNoDesc", "r"},
			[]string{"__completeNoDesc", "r"}},
		{"unknown word routes to auto", []string{"unknownword"},
			[]string{"auto", "--", "unknownword"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, routeArgs(tc.in))
		})
	}
}
