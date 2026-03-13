/*
 * Copyright 2025 NVIDIA CORPORATION.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package flags

import (
	"time"

	"github.com/urfave/cli/v2"
)

type LeaderElectionConfig struct {
	Enabled            bool
	LeaseLockName      string
	LeaseLockNamespace string
	LeaseDuration      time.Duration
	RenewDeadline      time.Duration
	RetryPeriod        time.Duration
}

func (l *LeaderElectionConfig) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{
			Category:    "Leader election:",
			Name:        "leader-election-enabled",
			Usage:       "Start a leader election client and gain leadership before executing the main loop. Enable this when running replicated components for high availability.",
			Value:       false,
			Destination: &l.Enabled,
			EnvVars:     []string{"LEADER_ELECTION_ENABLED"},
		},
		&cli.StringFlag{
			Category:    "Leader election:",
			Name:        "leader-election-lease-lock-namespace",
			Usage:       "The lease lock resource namespace.",
			Value:       "default",
			Destination: &l.LeaseLockNamespace,
			EnvVars:     []string{"LEADER_ELECTION_LEASE_LOCK_NAMESPACE"},
		},
		&cli.StringFlag{
			Category:    "Leader election:",
			Name:        "leader-election-lease-lock-name",
			Usage:       "The lease lock resource name.",
			Value:       "nvidia-compute-domain-controller",
			Destination: &l.LeaseLockName,
			EnvVars:     []string{"LEADER_ELECTION_LEASE_LOCK_NAME"},
		},
		&cli.DurationFlag{
			Category:    "Leader election:",
			Name:        "leader-election-lease-duration",
			Usage:       "The duration that non-leader candidates will wait to force acquire leadership. This is measured against time of last observed ack.",
			Value:       15 * time.Second,
			Destination: &l.LeaseDuration,
			EnvVars:     []string{"LEADER_ELECTION_LEASE_DURATION"},
		},
		&cli.DurationFlag{
			Category:    "Leader election:",
			Name:        "leader-election-renew-deadline",
			Usage:       "The duration that the acting controlplane will retry refreshing leadership before giving up.",
			Value:       10 * time.Second,
			Destination: &l.RenewDeadline,
			EnvVars:     []string{"LEADER_ELECTION_RENEW_DEADLINE"},
		},
		&cli.DurationFlag{
			Category:    "Leader election:",
			Name:        "leader-election-retry-period",
			Usage:       "The duration the LeaderElector clients should wait between tries of actions.",
			Value:       2 * time.Second,
			Destination: &l.RetryPeriod,
			EnvVars:     []string{"LEADER_ELECTION_RETRY_PERIOD"},
		},
	}
}
