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

package fnet_test

import (
	"testing"

	"github.com/santhosh-tekuri/fnet"
)

func TestFirewall_AllowSelf(t *testing.T) {
	nw := fnet.New()
	earth, mars, venus := nw.Host("earth"), nw.Host("mars"), nw.Host("venus")

	nw.SetFirewall(fnet.AllowSelf)
	elr, mlr, vlr := listen(t, earth, 80), listen(t, mars, 80), listen(t, venus, 80)
	if _, _, err := dial(elr, earth); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dial(mlr, mars); err != nil {
		t.Fatal(err)
	}
	if _, err := venus.Dial(mlr.Addr().String()); err == nil {
		t.Fatal()
	}
	if _, err := mars.Dial(vlr.Addr().String()); err == nil {
		t.Fatal()
	}
	if _, err := earth.Dial(mlr.Addr().String()); err == nil {
		t.Fatal("earth should not be able to dial mars")
	}
	if _, err := mars.Dial(elr.Addr().String()); err == nil {
		t.Fatal("mars should not be able to dial earth")
	}

	nw.SetFirewall(fnet.AllowAll)
	if _, _, err := dial(vlr, mars); err != nil {
		t.Fatal()
	}

	_, _, _ = elr, mlr, vlr
}

func TestFirewall_Split(t *testing.T) {
	nw := fnet.New()
	earth, mars, venus := nw.Host("earth"), nw.Host("mars"), nw.Host("venus")

	nw.SetFirewall(fnet.Split{
		Hosts: []string{"mars", "venus"},
		Next:  fnet.AllowAll,
	})
	elr, mlr, vlr := listen(t, earth, 80), listen(t, mars, 80), listen(t, venus, 80)
	if _, _, err := dial(elr, earth); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dial(mlr, mars); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dial(mlr, venus); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dial(vlr, mars); err != nil {
		t.Fatal(err)
	}
	if _, err := earth.Dial(mlr.Addr().String()); err == nil {
		t.Fatal("earth should not be able to dial mars")
	}
	if _, err := mars.Dial(elr.Addr().String()); err == nil {
		t.Fatal("mars should not be able to dial earth")
	}
}
