package main

import (
	"testing"
)

func TestIsError(t *testing.T) {
	bins := map[string]int{
		"E,!NO_DATA!,,":            4,
		"E,Unauthorized user ID.,": 3,
		"":                         0,
		"LH,2023-05-26,111.1100,111.1000,111.1000,111.1000,111111,0,": 0,
	}

	for line, num := range bins {
		if tok := isError([]byte(line)); len(tok) != num {
			t.Errorf("Line=%s len=%d", line, len(tok))
		}
	}

}
