// Copyright 2019 DxChain, All rights reserved.
// Use of this source code is governed by an Apache
// License 2.0 that can be found in the LICENSE file.

package storagehosttree

import (
	"math/rand"
	"testing"

	"github.com/DxChainNetwork/godx/p2p/enode"
)

var ips = []string{
	"99.0.86.9",
	"104.143.92.125",
	"104.237.91.15",
	"185.192.69.89",
	"104.238.46.146",
	"104.238.46.156",
}

var EnodeID = []enode.ID{}

var filter = NewFilter()

func TestFilter_Filtered(t *testing.T) {

	for _, ip := range ips {
		filter.Add(ip)
	}

	for _, ip := range ips {
		out := filter.Filtered(ip)
		if out != true {
			t.Errorf("error: the ip address %s should be filtered", ip)
		}
	}
}

func TestFilter_Reset(t *testing.T) {
	filter.Reset()

	for _, ip := range ips {
		out := filter.Filtered(ip)
		if out != false {
			t.Errorf("error: the ip address %s should not be filtered", ip)
		}
	}
}

func generageRandomByteArray() [32]byte {
	id := make([]byte, 32)
	rand.Read(id)
	var result [32]byte
	copy(result[:], id[:32])
	return result
}

func EnodeIDGenerater() {
	for i := 0; i < 6; i++ {
		EnodeID = append(EnodeID, generageRandomByteArray())
	}
}
