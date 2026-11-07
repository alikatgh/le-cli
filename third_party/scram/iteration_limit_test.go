// SPDX-License-Identifier: Apache-2.0
// Local regression tests for localhost-explorer's server work-factor bound.
package scram

import (
	"crypto/sha256"
	"errors"
	"hash"
	"strconv"
	"strings"
	"testing"
)

func TestExcessiveServerIterationsNeverStartDerivation(t *testing.T) {
	for _, iterations := range []int{100001, 2147483647} {
		calls := 0
		factory := HashGeneratorFcn(func() hash.Hash { calls++; return sha256.New() })
		client, err := factory.NewClientUnprepped("fixture", "fixture", "")
		if err != nil {
			t.Fatal(err)
		}
		client.WithNonceGenerator(func() string { return "fixtureNonce" })
		conversation := client.NewConversation()
		if _, err = conversation.Step(""); err != nil {
			t.Fatal(err)
		}
		response, err := conversation.Step("r=fixtureNonceServer,s=c2FsdA==,i=" + strconv.Itoa(iterations))
		if !errors.Is(err, ErrIterationLimit) || response != "" || calls != 0 {
			t.Fatal("excessive server work reached key derivation")
		}
	}
}

func TestIterationBoundaryAndMinimumRemainSupported(t *testing.T) {
	client, err := SHA256.NewClientUnprepped("fixture", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	client.WithNonceGenerator(func() string { return "fixtureNonce" })
	// A cached boundary value exercises policy acceptance without executing a
	// 100,000-iteration test workload. Ordinary SCRAM still derives below.
	client.cache[KeyFactors{Salt: "salt", Iters: 100000}] = derivedKeys{StoredKey: []byte("stored"), ClientKey: make([]byte, 32), ServerKey: []byte("server")}
	conversation := client.NewConversation()
	if _, err = conversation.Step(""); err != nil {
		t.Fatal(err)
	}
	if response, err := conversation.Step("r=fixtureNonceServer,s=c2FsdA==,i=100000"); err != nil || !strings.Contains(response, ",p=") {
		t.Fatal("allowed boundary was rejected")
	}
	conversation = client.NewConversation()
	if _, err = conversation.Step(""); err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.Step("r=fixtureNonceServer,s=c2FsdA==,i=4095"); err == nil {
		t.Fatal("minimum iteration check lost")
	}
	conversation = client.NewConversation()
	if _, err = conversation.Step(""); err != nil {
		t.Fatal(err)
	}
	if response, err := conversation.Step("r=fixtureNonceServer,s=c2FsdA==,i=4096"); err != nil || !strings.Contains(response, ",p=") {
		t.Fatal("ordinary SCRAM derivation failed")
	}
}
