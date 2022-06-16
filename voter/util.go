/**
 * Copyright (C) 2021 The poly network Authors
 * This file is part of The poly network library.
 *
 * The poly network is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Lesser General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * The poly network is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Lesser General Public License for more details.
 *
 * You should have received a copy of the GNU Lesser General Public License
 * along with the poly network.  If not, see <http://www.gnu.org/licenses/>.
 *
 */
package voter

import (
	"math/rand"
	"sync"
	"time"

	"github.com/ontio/ontology-crypto/ec"
	"github.com/ontio/ontology-crypto/keypair"
)

func zionPrivateKey2Hex(pri keypair.PrivateKey) []byte {
	switch t := pri.(type) {
	case *ec.PrivateKey:
		switch t.Algorithm {
		case ec.ECDSA:

		default:
			panic("unsupported pk")
		}
		Nlen := (t.Params().BitSize + 7) >> 3
		skBytes := t.D.Bytes()
		skEncoded := make([]byte, Nlen)
		// pad sk with zeroes
		copy(skEncoded[Nlen-len(skBytes):], skBytes)
		return skEncoded
	default:
		panic("unknown private key type")
	}
}

// GoFunc runs a goroutine under WaitGroup
func GoFunc(routinesGroup *sync.WaitGroup, f func()) {
	routinesGroup.Add(1)
	go func() {
		defer routinesGroup.Done()
		f()
	}()
}

func sleep() {
	time.Sleep(time.Second)
}

func randIdx(size int) int {
	return int(rand.Uint32()) % size
}
