package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/rs/zerolog/log"
)

type deviceRuntime struct {
	AttemptedAt int64           `json:"attempted_at"`
	CollectedAt int64           `json:"collected_at"`
	Status      string          `json:"status"`
	Info        json.RawMessage `json:"info"`
	Error       string          `json:"error,omitempty"`
}

// Caller holds the tenant lock, including API callers after access checks.
func (a *TenantAuth) runtimeLocked(id string) deviceRuntime {
	result := deviceRuntime{Status: "pending", Info: json.RawMessage("{}")}
	var info string
	if a.db.QueryRow("SELECT attempted_at,collected_at,status,info,error FROM device_runtime WHERE device=?", id).
		Scan(&result.AttemptedAt, &result.CollectedAt, &result.Status, &info, &result.Error) == nil {
		result.Info = json.RawMessage(info)
	}
	return result
}

func (a *TenantAuth) collectRuntime(dev *Device) {
	attempt := randomID()
	a.mu.Lock()
	_, err := a.db.Exec("INSERT INTO device_runtime(device,attempt,attempted_at,status) VALUES(?,?,?,'collecting') ON CONFLICT(device) DO UPDATE SET attempt=excluded.attempt,attempted_at=excluded.attempted_at,status='collecting',error=''", dev.id, attempt, time.Now().Unix())
	profile, profileErr := a.profileLocked(dev.id)
	a.mu.Unlock()
	if err != nil {
		log.Error().Err(err).Msg("record device runtime collection")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(dev.ctx, 20*time.Second)
		defer cancel()
		var info json.RawMessage
		var collectErr error
		if profileErr != nil {
			collectErr = errors.New("尚未配置运维执行账号")
		} else if dev.proto < 6 {
			collectErr = errors.New("旧版 rtty 命令需要设备密码，请升级 Go rtty 客户端以自动采集")
		} else {
			select {
			case operationSlots <- struct{}{}:
				defer func() { <-operationSlots }()
				info, collectErr = dev.runtimeInfo(ctx, profile)
			case <-ctx.Done():
				collectErr = ctx.Err()
			}
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if collectErr != nil {
			// Preserve the last successful snapshot and its original timestamp.
			_, err = a.db.Exec("UPDATE device_runtime SET status='failed',error=? WHERE device=? AND attempt=?", collectErr.Error(), dev.id, attempt)
		} else {
			_, err = a.db.Exec("UPDATE device_runtime SET status='ready',error='',info=?,collected_at=? WHERE device=? AND attempt=?", string(info), time.Now().Unix(), dev.id, attempt)
		}
		if err != nil {
			log.Error().Err(err).Msg("save device runtime collection")
		}
	}()
}

func (dev *Device) runtimeInfo(ctx context.Context, profile operationProfile) (json.RawMessage, error) {
	raw, err := dev.runOperation(ctx, profile, map[string]any{"action": "psdk_version"})
	if err != nil {
		return nil, err
	}
	var response struct {
		Err    int    `json:"err"`
		Code   int    `json:"code"`
		Stdout string `json:"stdout"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Err != 0 {
		return nil, errors.New("客户端无法执行版本采集")
	}
	output, err := base64.StdEncoding.DecodeString(response.Stdout)
	var info struct {
		Container      string `json:"container"`
		Image          string `json:"image"`
		ImageID        string `json:"image_id"`
		ProgramVersion string `json:"program_version"`
		Error          string `json:"error"`
	}
	if err != nil || len(output) > 4096 || json.Unmarshal(output, &info) != nil {
		return nil, errors.New("版本采集输出无效或设备缺少 Python 3")
	}
	if info.Error != "" {
		return nil, errors.New(info.Error)
	}
	if response.Code != 0 || info.Container != "ja-uav-psdk" || info.Image == "" || info.ImageID == "" {
		return nil, errors.New("版本采集未返回运行中的 PSDK 镜像")
	}
	return json.Marshal(info)
}
