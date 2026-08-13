package main

import "testing"

func TestLogChannelsFlagString(t *testing.T) {
	channels := logChannelsFlag{-1001098030726: -1001089898989}

	const expected = "-1001098030726:-1001089898989"
	if actual := channels.String(); actual != expected {
		t.Fatalf("expected %q, got %q", expected, actual)
	}
}
