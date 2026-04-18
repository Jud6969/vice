package stt

import (
	"testing"

	av "github.com/mmp/vice/aviation"
)

func TestFrequencyParser(t *testing.T) {
	p := &frequencyParser{}
	cases := []struct {
		name   string
		tokens []Token
		want   av.Frequency
		cons   int
	}{
		{
			name: "point_two_digit",
			tokens: []Token{
				{Type: TokenNumber, Value: 1}, {Type: TokenNumber, Value: 2},
				{Type: TokenNumber, Value: 7}, {Type: TokenWord, Text: "point"},
				{Type: TokenNumber, Value: 7}, {Type: TokenNumber, Value: 5},
			},
			want: 127750, cons: 6,
		},
		{
			name: "point_three_digit",
			tokens: []Token{
				{Type: TokenNumber, Value: 1}, {Type: TokenNumber, Value: 2},
				{Type: TokenNumber, Value: 7}, {Type: TokenWord, Text: "point"},
				{Type: TokenNumber, Value: 7}, {Type: TokenNumber, Value: 5},
				{Type: TokenNumber, Value: 0},
			},
			want: 127750, cons: 7,
		},
		{
			name: "five_digits_no_point",
			tokens: []Token{
				{Type: TokenNumber, Value: 1}, {Type: TokenNumber, Value: 2},
				{Type: TokenNumber, Value: 7}, {Type: TokenNumber, Value: 7},
				{Type: TokenNumber, Value: 5},
			},
			want: 127750, cons: 5,
		},
		{
			name: "six_digits_no_point",
			tokens: []Token{
				{Type: TokenNumber, Value: 1}, {Type: TokenNumber, Value: 2},
				{Type: TokenNumber, Value: 7}, {Type: TokenNumber, Value: 7},
				{Type: TokenNumber, Value: 5}, {Type: TokenNumber, Value: 0},
			},
			want: 127750, cons: 6,
		},
		{
			name: "out_of_band_rejected",
			tokens: []Token{
				{Type: TokenNumber, Value: 1}, {Type: TokenNumber, Value: 0},
				{Type: TokenNumber, Value: 0}, {Type: TokenWord, Text: "point"},
				{Type: TokenNumber, Value: 0}, {Type: TokenNumber, Value: 0},
			},
			want: 0, cons: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, cons, _ := p.parse(tc.tokens, 0, Aircraft{})
			if tc.want == 0 {
				if got != nil {
					t.Errorf("expected rejection, got %v", got)
				}
				return
			}
			f, ok := got.(av.Frequency)
			if !ok {
				t.Fatalf("got %T, want av.Frequency", got)
			}
			if f != tc.want {
				t.Errorf("freq = %d, want %d", f, tc.want)
			}
			if cons != tc.cons {
				t.Errorf("consumed = %d, want %d", cons, tc.cons)
			}
		})
	}
}
