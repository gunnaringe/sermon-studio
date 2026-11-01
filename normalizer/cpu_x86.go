//go:build 386 || amd64

package normalizer

import "golang.org/x/sys/cpu"

func canTranscribe() bool { return cpu.X86.HasAVX && cpu.X86.HasF16C }
