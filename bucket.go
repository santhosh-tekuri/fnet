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
	mu sync.Mutex
	Bandwidth
	tokens int64
	time   time.Time
}

func newBucket(bw Bandwidth) *bucket {
	if bw == NoLimit {
		return nil
	}
	return &bucket{Bandwidth: bw, time: time.Now()}
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

func (b *bucket) waitTime(now time.Time, deadline time.Time) (sleep time.Duration, timeout bool) {
	if deadline.IsZero() || deadline.After(b.time) {
		return b.time.Sub(now), false
	}
	return deadline.Sub(now), true
}

func (b *bucket) remove(n int64) time.Time {
	n -= b.tokens
	b.tokens = 0
	b.time = b.time.Add(b.durationFor(n))
	return b.time
}

func (b *bucket) taken(n int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.update(time.Now())
	b.remove(n)
}

// request n tokens with given user deadline
// returns:
//      - amount of duration to sleep before doing any action
//      - if zero tokens, return timeout to user
//      - use the tokens with the new deadline returned
//      - in case take==false, report how many has been taken by calling bucket.taken method
func (b *bucket) request(take bool, n int64, deadline time.Time) (time.Duration, int64, time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	b.update(now)
	sleep, timeout := b.waitTime(now, deadline)
	if timeout {
		return sleep, 0, deadline
	}
	now = b.time

	n = min(n, b.maxTokensFor(deadline))
	if take {
		b.remove(n)
	}
	return sleep, n, now.Add(b.durationFor(n))
}

func min(a, b int64) int64 {
	if a <= b {
		return a
	}
	return b
}
