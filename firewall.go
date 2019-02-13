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

// Firewall is used to decide whether to allow or block
// network traffic between given two hosts
type Firewall interface {
	Allow(host1, host2 string) bool
}

type all bool

func (a all) Allow(host1, host2 string) bool {
	return bool(a) || host1 == host2
}

const (
	// AllowAll firewall does not block any network traffic.
	// This is the default firewall set on newly created network.
	AllowAll all = true

	// AllowSelf firewall blocks traffic between distinct hosts.
	// Note that traffic within the host is allowed.
	//
	// Consider network with hosts m1, m2, m3 and m4
	// AllowSelf creates following network partitions:
	//     m1 | m2 | m3 | m4
	AllowSelf all = false
)

// Isolate partitions the hosts in network into two groups:
// one containing the given hosts, another containing rest of the hosts.
// hosts in a group can communicate with each other, but they cannot
// communicate with hosts in other group.
//
//  // Consider network with hosts m1, m2, m3, m4 and m5
//
//  // 2 partitions: m1 m2 | m3 m4 m5
//  Isolate("m1", "m2")
func Isolate(hosts ...string) Split {
	return Split{
		Hosts: hosts,
		Next:  AllowAll,
	}
}

// Split firewall implements network partioning. Mutiple partitions
// can be defined by chaining. See example below:
//
//  // Consider network with hosts m1, m2, m3, m4, m5 and m6
//
//  // 2 partitions: m1 m2 | m3 m4 m5 m6
//  Split{
//      Hosts: []string{"m1", "m2"},
//      Next: AllowAll,
//  }
//
//  // 3 partitions: m1 m2 | m3 m4 | m5 m6
//  Split{
//      Hosts: []string{"m1", "m2"},
//      Next: Split {
//          Hosts: []string{"m3", "m4"},
//          Next: AllowAll,
//      },
//  }
type Split struct {
	// Hosts is list of hosts in partition
	Hosts []string

	// Next firewall is used when given two hosts
	// are not in the above partition
	Next Firewall
}

// Allow implements Firewall.Allow
func (s Split) Allow(host1, host2 string) bool {
	b1, b2 := contains(s.Hosts, host1), contains(s.Hosts, host2)
	switch {
	case b1 && b2:
		return true
	case b1 || b2:
		return false
	}
	return s.Next.Allow(host1, host2)
}

func contains(hosts []string, host string) bool {
	for _, h := range hosts {
		if h == host {
			return true
		}
	}
	return false
}
