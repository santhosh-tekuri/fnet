// Copyright 2019 Santhosh Kumar Tekuri
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package fnet

import (
	"fmt"
	"testing"
	"time"
)

func TestBandwidth(t *testing.T) {
	tests := []struct {
		bw       Bandwidth
		duration time.Duration
		tokens   int64
	}{
		{1e9, 5000, 5000},
		{1e9, 10, 10},
		{1e9 / 2, 2 * 5000, 5000},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("Bandwidth %v", test.bw), func(t *testing.T) {
			if got := test.bw.durationFor(test.tokens); got != test.duration {
				t.Errorf("durationFor: got %d, want %d", got, test.duration)
			}
			if got := test.bw.tokensFor(test.duration); got != test.tokens {
				t.Errorf("tokensFor: got %d, want %d", got, test.tokens)
			}
		})
	}
}

func TestBucket_maxTokensFor(t *testing.T) {
	bw, now := Bandwidth(1e9), time.Now()
	tests := []struct {
		name     string
		deadline time.Time
		want     int64
	}{
		{"no deadline", time.Time{}, bw.burst()},
		{"deadline larget than burstTime", now.Add(time.Hour), bw.burst()},
		{"deadline smaller than burstTime", now.Add(10 * time.Nanosecond), 10},
		{"deadline equals burstTime", now.Add(time.Second), bw.burst()},
		{"deadline before bucketTime", now.Add(-time.Minute), 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			b := newBucket(bw)
			b.time = now
			if got := b.maxTokensFor(test.deadline); got != test.want {
				t.Errorf("got %d, want %d", got, test.want)
			}
			b.tokens = 500
			if got := b.maxTokensFor(test.deadline); got != test.want {
				t.Errorf("got %d, want %d", got, test.want)
			}
		})
	}
}

func TestBandwidth_update(t *testing.T) {
	b := newBucket(Bandwidth(1e9))
	now := b.time

	// update to smaller than brustTime
	now = now.Add(10 * time.Nanosecond)
	b.update(now)
	if b.time != now {
		t.Fatalf("got %d, want %d", b.time.Nanosecond(), now.Nanosecond())
	}
	if b.tokens != 10 {
		t.Fatalf("got %d, want %d", b.tokens, 10)
	}

	// update to past
	b.update(now.Add(-5 * time.Second))
	if b.time != now {
		t.Fatalf("got %d, want %d", b.time.Nanosecond(), now.Nanosecond())
	}
	if b.tokens != 10 {
		t.Fatalf("got %d, want %d", b.tokens, 10)
	}

	// update to larger than brustTime
	now = now.Add(time.Hour)
	b.update(now)
	if b.time != now {
		t.Fatalf("got %d, want %d", b.time.Nanosecond(), now.Nanosecond())
	}
	if b.tokens != b.burst() {
		t.Fatalf("got %d, want %d", b.tokens, b.burst())
	}
}
