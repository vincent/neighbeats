package main

import (
	"flag"
	"io"
	"reflect"
	"testing"
)

func TestParseInterspersed(t *testing.T) {
	cases := []struct {
		args  []string
		words []string
		n     int
	}{
		{[]string{"vitalic", "mort", "-n", "5"}, []string{"vitalic", "mort"}, 5},
		{[]string{"-n", "7", "vitalic", "mort"}, []string{"vitalic", "mort"}, 7},
		{[]string{"vitalic", "-n", "3", "mort"}, []string{"vitalic", "mort"}, 3},
		{[]string{"-n", "2", "--", "-dash", "word"}, []string{"-dash", "word"}, 2},
		{nil, nil, 20},
	}
	for _, c := range cases {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		n := fs.Int("n", 20, "")
		got, err := parseInterspersed(fs, c.args)
		if err != nil || *n != c.n || !reflect.DeepEqual(got, c.words) {
			t.Errorf("%v -> words %v n %d err %v; want %v %d", c.args, got, *n, err, c.words, c.n)
		}
	}
}
