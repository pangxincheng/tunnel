package tunnel

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Device 是服务器上的一台设备:设备信息加在线状态。
type Device struct {
	DeviceInfo
	Online bool `json:"online"`
}

const (
	probeTimeout     = 3 * time.Second
	probeConcurrency = 8
	sockSuffix       = ".sock"
)

// ListDevices 列出 dir 下所有 *.json 与 *.sock 对应的设备(只有 socket 没有
// json 的设备仅 Name 有值),并发探测每个 socket 是否在线,按 Name 升序返回。
// ctx 被取消时返回 ctx.Err()。
func ListDevices(ctx context.Context, sc *ssh.Client, dir string) ([]Device, error) {
	infos, err := ReadDeviceInfos(sc, dir)
	if err != nil {
		return nil, err
	}
	socks, err := listSocketNames(sc, dir)
	if err != nil {
		return nil, err
	}
	for _, n := range socks {
		if _, ok := infos[n]; !ok {
			infos[n] = DeviceInfo{Name: n}
		}
	}
	devices := make([]Device, 0, len(infos))
	for key, info := range infos {
		// 文件名是身份与探测的依据,不信任 json 内的 name(可能为空或不一致)
		info.Name = key
		devices = append(devices, Device{DeviceInfo: info})
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Name < devices[j].Name })

	sem := make(chan struct{}, probeConcurrency)
	var wg sync.WaitGroup
	for i := range devices {
		wg.Add(1)
		sem <- struct{}{}
		go func(d *Device) {
			defer wg.Done()
			defer func() { <-sem }()
			d.Online = probeSocket(ctx, sc, path.Join(dir, d.Name+sockSuffix))
		}(&devices[i])
	}
	wg.Wait()
	// 取消时探测结果不可信(全部记为离线),不能当作「全部离线」返回
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return devices, nil
}

func listSocketNames(sc *ssh.Client, dir string) ([]string, error) {
	c, err := sftp.NewClient(sc)
	if err != nil {
		return nil, fmt.Errorf("打开 SFTP: %w", err)
	}
	defer c.Close()
	entries, err := c.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("列出 %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), sockSuffix) {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), sockSuffix))
	}
	return names, nil
}

// probeSocket 拨号 sock,成功即在线(立刻关闭);3 秒超时或 ctx 取消视为离线。
func probeSocket(ctx context.Context, sc *ssh.Client, sock string) bool {
	type result struct{ ok bool }
	ch := make(chan result, 1)
	go func() {
		conn, err := sc.Dial("unix", sock)
		if err == nil {
			conn.Close()
		}
		ch <- result{err == nil}
	}()
	timer := time.NewTimer(probeTimeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.ok
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}
