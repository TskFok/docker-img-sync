package sync

import "errors"

const (
	PhaseDigest   = "digest"
	PhaseManifest = "manifest"
	PhaseCopy     = "copy"
)

const phaseSaveMessage = "保存同步阶段失败"

var errPhaseSave = errors.New(phaseSaveMessage)

func PhaseText(phase string) string {
	switch phase {
	case PhaseDigest:
		return "检查摘要"
	case PhaseManifest:
		return "读取清单"
	case PhaseCopy:
		return "复制镜像"
	case "":
		return ""
	default:
		return phase
	}
}
