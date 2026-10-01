package store

import (
	"reflect"
	"testing"
)

func TestEqualShares(t *testing.T) {
	cases := []struct {
		amount int64
		n      int
		want   []int64
	}{
		{1000, 3, []int64{334, 333, 333}},
		{1, 3, []int64{1, 0, 0}},
		{10, 3, []int64{4, 3, 3}},
		{999, 3, []int64{333, 333, 333}},
		{5, 5, []int64{1, 1, 1, 1, 1}},
	}
	for _, c := range cases {
		if got := EqualShares(c.amount, c.n); !reflect.DeepEqual(got, c.want) {
			t.Errorf("EqualShares(%d,%d) = %v, want %v", c.amount, c.n, got, c.want)
		}
	}
}

func TestDerivedHandle(t *testing.T) {
	if got := DerivedHandle("Ada.Lovelace+x@Example.com"); got != "ada_lovelace_x" {
		t.Errorf("got %q", got)
	}
	if got := DerivedHandle("abcdefghijklmnopqrstuvwxyz@x.y"); got != "abcdefghijklmnopqrst" {
		t.Errorf("got %q", got)
	}
}
