package main

import (
	"encoding/json"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager/checksum"
)

type Checkpoint struct {
	Checksum checksum.Checksum `json:"checksum"`
	V1       *CheckpointV1     `json:"v1,omitempty"`
}

type CheckpointV1 struct {
	PreparedClaims PreparedClaimsByUID `json:"preparedClaims,omitempty"`
}

// key: stringified claim UUID
type PreparedClaimsByUID map[string]PreparedClaim

type PreparedClaim struct {
	Status          resourceapi.ResourceClaimStatus `json:"status,omitempty"`
	PreparedDevices PreparedDevices                 `json:"preparedDevices,omitempty"`
}

func newCheckpoint() *Checkpoint {
	pc := &Checkpoint{
		Checksum: 0,
		V1: &CheckpointV1{
			PreparedClaims: make(PreparedClaimsByUID),
		},
	}
	return pc
}

func (cp *Checkpoint) MarshalCheckpoint() ([]byte, error) {
	cp.Checksum = 0
	out, err := json.Marshal(*cp)
	if err != nil {
		return nil, err
	}
	cp.Checksum = checksum.New(out)
	return json.Marshal(*cp)
}

func (cp *Checkpoint) UnmarshalCheckpoint(data []byte) error {
	return json.Unmarshal(data, cp)
}

func (cp *Checkpoint) VerifyChecksum() error {
	ck := cp.Checksum
	cp.Checksum = 0
	defer func() {
		cp.Checksum = ck
	}()
	out, err := json.Marshal(*cp)
	if err != nil {
		return err
	}
	return ck.Verify(out)
}
