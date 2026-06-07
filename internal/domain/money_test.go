package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/evt/chefhub/internal/domain"
)

func TestParseMoney(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want domain.Money
	}{
		{name: "whole dollar", in: "12", want: 1200},
		{name: "two decimals", in: "12.34", want: 1234},
		{name: "one decimal", in: "12.3", want: 1230},
		{name: "more than two decimals rounds half-up", in: "12.345", want: 1235},
		{name: "more than two decimals rounds down", in: "12.344", want: 1234},
		{name: "leading plus", in: "+5.00", want: 500},
		{name: "negative", in: "-5.05", want: -505},
		{name: "whitespace", in: "  7.50 ", want: 750},
		{name: "zero", in: "0.00", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.ParseMoney(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseMoneyErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "letters", in: "abc"},
		{name: "junk in fractional", in: "12.ab"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := domain.ParseMoney(tt.in)
			require.Error(t, err)
			assert.ErrorIs(t, err, domain.ErrInvalidAmount)
		})
	}
}

func TestMoneyDecimal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   domain.Money
		want string
	}{
		{name: "zero", in: 0, want: "0.00"},
		{name: "round dollar", in: 500, want: "5.00"},
		{name: "with cents", in: 1234, want: "12.34"},
		{name: "negative", in: -505, want: "-5.05"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.in.Decimal())
		})
	}
}

func TestMoneyMarshalJSON(t *testing.T) {
	t.Parallel()

	got, err := domain.Money(1234).MarshalJSON()
	require.NoError(t, err)
	assert.Equal(t, `"12.34"`, string(got))
}
