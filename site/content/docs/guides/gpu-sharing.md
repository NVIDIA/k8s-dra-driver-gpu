---
title: GPU sharing
linkTitle: GPU sharing
weight: 10
description: Sharing GPUs across pods with time-slicing, MPS, and MIG.
---

{{% pageinfo %}}
Coming soon — this page will cover the three GPU-sharing modes the driver
supports:

- **Time-slicing** — round-robin scheduling of full GPUs across pods.
- **MPS** — NVIDIA Multi-Process Service for concurrent kernel execution.
- **MIG** — hardware-partitioned Multi-Instance GPU slices.

It will explain when to pick each, the trade-offs, and how to request them
through `ResourceClaim`s.
{{% /pageinfo %}}