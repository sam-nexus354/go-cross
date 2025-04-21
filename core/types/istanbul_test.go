// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package types

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

func TestHeaderHash(t *testing.T) {
	// 0xd848102c76ea4c0a814cd8501ee5e2f243d4d7a0c6a2bba68a4be23ca1f80965
	expectedExtra := common.FromHex("0x0000000000000000000000000000000000000000000000000000000000000000f89af8549444add0ec310f115a0e603b2d7db9f067778eaf8a94294fc7e8f22b3bcdcf955dd7ff3ba2ed833f8212946beaaed781d2d2ab6350f5c4566a2c6eaac407a6948be76812f765c24641ec63dc2852b378aba2b440b8410000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000c0")
	expectedHash := common.HexToHash("0xd848102c76ea4c0a814cd8501ee5e2f243d4d7a0c6a2bba68a4be23ca1f80965")

	// for istanbul consensus
	header := &Header{MixDigest: IstanbulDigest, Extra: expectedExtra}
	if !reflect.DeepEqual(header.Hash(), expectedHash) {
		t.Errorf("expected: %v, but got: %v", expectedHash.Hex(), header.Hash().Hex())
	}

	// append useless information to extra-data
	unexpectedExtra := append(expectedExtra, []byte{1, 2, 3}...)
	header.Extra = unexpectedExtra
	if !reflect.DeepEqual(header.Hash(), rlpHash(header)) {
		t.Errorf("expected: %v, but got: %v", rlpHash(header).Hex(), header.Hash().Hex())
	}
}

func TestExtractToIstanbulExtra(t *testing.T) {
	testCases := []struct {
		istRawData     []byte
		expectedResult *IstanbulExtra
		expectedErr    error
	}{
		{
			// normal case
			hexutil.MustDecode("0xf85b80f85494dac22e5518f5fb45d54f4273a74ec65bfb7d9d70942bd86dd271331a2d88eb26d29cf64769a7e58ac59429e52f8bcf83e91db155a3e7bcbb515c503868d494329eb77663d658e946f481aeb6729664e99c8cc7c080c080"),
			&IstanbulExtra{
				VanityData: []byte{},
				Validators: []common.Address{
					common.BytesToAddress(hexutil.MustDecode("0xcac22e5518f5fb45d54f4273a74ec65bfb7d9d70")),
					common.BytesToAddress(hexutil.MustDecode("0x2bd86dd271331a2d88eb26d29cf64769a7e58ac5")),
					common.BytesToAddress(hexutil.MustDecode("0x29e52f8bcf83e91db155a3e7bcbb515c503868d4")),
					common.BytesToAddress(hexutil.MustDecode("0x329eb77663d658e946f481aeb6729664e99c8cc7")),
				},
				CommittedSeal: [][]byte{},
				Round:         0,
				Vote:          nil,
				RandomReveal:  []byte{},
			},
			nil,
		},
	}
	for _, test := range testCases {
		h := &Header{Extra: test.istRawData}
		istanbulExtra, err := ExtractIstanbulExtra(h)
		if err != test.expectedErr {
			t.Errorf("expected: %v, but got: %v", test.expectedErr, err)
		}
		fmt.Printf("Extracted IstanbulExtra: %+v\n", istanbulExtra)
		if !reflect.DeepEqual(istanbulExtra, test.expectedResult) {
			t.Errorf("expected: %v, but got: %v", test.expectedResult, istanbulExtra)
		}
	}
}
