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

// Split firewall implements network partioning. Mutiple partitions
// can be defined by chaining. See example below:
//
//  // Consider network with hosts m1, m2, m3, m4, m5 and m6
//
//  // 2 partitions: m1 m2 | m3 m4 m5 m6
//  firewall := fnet.Split([]string{"m1", "m2"}, fnet.AllowAll)
//
//  // 3 partitions: m1 m2 | m3 m4 | m5 m6
//  firewall := fnet.Split([]string{"m1", "m2"}, fnet.AllowAll)
//  firewall = fnet.Split([]string{"m3", "m4"}, firewall) // chaining
func Split(hosts []string, next Firewall) Firewall {
	return split{
		hosts: hosts,
		next:  AllowAll,
	}
}

type split struct {
	// Hosts is list of hosts in partition
	hosts []string

	// Next firewall is used when given two hosts
	// are not in the above partition
	next Firewall
}

// Allow implements Firewall.Allow
func (s split) Allow(host1, host2 string) bool {
	b1, b2 := contains(s.hosts, host1), contains(s.hosts, host2)
	switch {
	case b1 && b2:
		return true
	case b1 || b2:
		return false
	}
	return s.next.Allow(host1, host2)
}

func contains(hosts []string, host string) bool {
	for _, h := range hosts {
		if h == host {
			return true
		}
	}
	return false
}
