/*
Copyright The Kubernetes Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"encoding/json"
	"fmt"

	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager/checksum"
)

type Checkpoint struct {
	// Note: The Checksum below is only associated with the V1 checkpoint
	// (because it doesn't have an embedded one). All future versions have
	// their checksum directly embedded in them to better support
	// downgrades. This checksum will be removed once the V1 checkpoint is
	// no longer supported.
	Checksum checksum.Checksum `json:"checksum"`
	V1       *CheckpointV1     `json:"v1,omitempty"`
	V2       *CheckpointV2     `json:"v2,omitempty"`
}

func (cp *Checkpoint) ToLatestVersion() *Checkpoint {
	latest := &Checkpoint{}
	switch {
	case cp.V2 != nil:
		latest.V2 = cp.V2
	case cp.V1 != nil:
		latest.V2 = cp.V1.ToV2()
	default:
		latest.V2 = &CheckpointV2{}
	}
	if latest.V2.PreparedClaims == nil {
		latest.V2.PreparedClaims = make(PreparedClaimsByUID)
	}
	return latest
}

func (cp *Checkpoint) MarshalCheckpoint() ([]byte, error) {
	cp = cp.ToLatestVersion()
	cp.V1 = cp.V2.ToV1()
	if err := cp.SetChecksumV1(); err != nil {
		return nil, fmt.Errorf("error setting v1 checksum: %v", err)
	}
	if err := cp.SetChecksumV2(); err != nil {
		return nil, fmt.Errorf("error setting v2 checksum: %v", err)
	}
	return json.Marshal(*cp)
}

func (cp *Checkpoint) SetChecksumV1() error {
	v2 := cp.V2
	cp.V2 = nil
	defer func() {
		cp.V2 = v2
	}()

	cp.Checksum = 0
	out, err := json.Marshal(*cp)
	if err != nil {
		return err
	}
	cp.Checksum = checksum.New(out)
	return nil
}

func (cp *Checkpoint) SetChecksumV2() error {
	cp.V2.Checksum = 0
	out, err := json.Marshal(*cp.V2)
	if err != nil {
		return err
	}
	cp.V2.Checksum = checksum.New(out)
	return nil
}

func (cp *Checkpoint) UnmarshalCheckpoint(data []byte) error {
	return json.Unmarshal(data, cp)
}

func (cp *Checkpoint) VerifyChecksum() error {
	if err := cp.VerifyChecksumV1(); err != nil {
		return err
	}
	if err := cp.VerifyChecksumV2(); err != nil {
		return err
	}
	return nil
}

func (cp *Checkpoint) VerifyChecksumV1() error {
	ck := cp.Checksum
	v2 := cp.V2
	cp.V2 = nil
	defer func() {
		cp.Checksum = ck
		cp.V2 = v2
	}()

	cp.Checksum = 0
	out, err := json.Marshal(*cp)
	if err != nil {
		return err
	}

	return ck.Verify(out)
}

func (cp *Checkpoint) VerifyChecksumV2() error {
	if cp.V2 == nil {
		return nil
	}

	ck := cp.V2.Checksum
	defer func() {
		cp.V2.Checksum = ck
	}()
	cp.V2.Checksum = 0
	out, err := json.Marshal(*cp.V2)
	if err != nil {
		return err
	}
	return ck.Verify(out)
}

func (cp *Checkpoint) GetNodeBootID() string {
	if cp.V2 != nil {
		return cp.V2.NodeBootID
	}
	return ""
}
