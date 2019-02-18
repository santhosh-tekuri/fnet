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
	"math"
	"sync"
	"time"
)

// NoLimit bandwidth represents no limits enforced.
//
// This is the default bandwidth set on newly created network.
var NoLimit = Bandwidth(math.MaxFloat64)

// Bandwidth represents network traffic in number of bytes per second.
type Bandwidth float64

func (b Bandwidth) durationFor(n int64) time.Duration {
	seconds := float64(n) / float64(b)
	return time.Duration(1e9 * seconds)
}

func (b Bandwidth) tokensFor(d time.Duration) int64 {
	return int64(d.Seconds() * float64(b))
}

func (b Bandwidth) burst() int64 {
	return int64(b)
}

type bucket struct {
	Bandwidth

	mu     sync.Mutex
	tokens int64
	time   time.Time
}

func newBucket(bw Bandwidth) *bucket {
	if bw == NoLimit {
		return nil
	}
	return &bucket{Bandwidth: bw, time: timeNow()}
}

// how many max tokens can be taken now, for given deadline
// returns:
//    - max tokens you can get. <=burst
//    - deadline for consuming them. <= given deadline
func (b *bucket) maxTokensFor(deadline time.Time) int64 {
	var max int64
	switch {
	case deadline.IsZero():
		max = b.burst()
	case deadline.Before(b.time):
		max = 0
	default:
		max = min(b.burst(), b.tokensFor(deadline.Sub(b.time)))
	}
	return max
}

// if bucket is stale (i.e, b.time<now), update it
func (b *bucket) update(now time.Time) {
	if now.Before(b.time) {
		return
	}
	if b.tokens < b.burst() {
		b.tokens += b.tokensFor(now.Sub(b.time))
		if b.tokens > b.burst() {
			b.tokens = b.burst()
		}
	}
	b.time = now
}

func (b *bucket) waitTime(now time.Time, deadline time.Time) time.Duration {
	if deadline.IsZero() || deadline.After(b.time) {
		return b.time.Sub(now)
	}
	return deadline.Sub(now)
}

func (b *bucket) remove(n int64) {
	free := min(n, b.tokens)
	n -= free
	b.tokens -= free
	if n > 0 {
		b.time = b.time.Add(b.durationFor(n))
	}
}

// request n tokens with given user deadline
// returns:
//      - duration to sleep. if >0 sleep and do request again
//      - if zero tokens, return timeout to user
//      - use the tokens with the new deadline returned
func (b *bucket) request(n int64, deadline time.Time) (time.Duration, int64, time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := timeNow()
	b.update(now)
	if wt := b.waitTime(now, deadline); wt > 0 {
		return wt, 0, deadline // sleep till you catch up b.time/deadline whichever is smaller
	}

	n = min(n, b.maxTokensFor(deadline))
	if n == 0 { // time to deadline is too small to get any tokens
		return deadline.Sub(now), 0, deadline // sleep till deadline
	}
	b.remove(n)
	return 0, n, now.Add(b.durationFor(n))
}

func min(a, b int64) int64 {
	if a <= b {
		return a
	}
	return b
}

// mocked during tests
var timeNow = func() time.Time {
	return time.Now()
}
