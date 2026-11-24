package tunnel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	controlCallTimeout = 3 * time.Second // ControlCall 总超时
	controlConnTimeout = 5 * time.Second // 服务端单个连接的读写总时限,防止卡住的客户端占用资源
	controlMaxRequest  = 4096            // 请求行最大字节数
)

// ControlRequest 是本机控制通道的请求。Op ∈ status|pause|resume|retry|set-name。
type ControlRequest struct {
	Op   string `json:"op"`
	Name string `json:"name,omitempty"`
}

// ControlResponse 是控制通道的响应。
type ControlResponse struct {
	OK     bool         `json:"ok"`
	Error  string       `json:"error,omitempty"`
	Status *ShareStatus `json:"status,omitempty"`
}

// ServeControl 在 l 上服务控制请求,直到 ctx 取消(关闭 l 并等待进行中的连接结束后返回 nil)。
// 协议:每个连接一行 JSON 请求、一行 JSON 响应,然后关闭。
// set-name 先调用 s.SetName,成功后再调用 onSetName(可为 nil)持久化。
func ServeControl(ctx context.Context, l net.Listener, s *Share, onSetName func(string) error) error {
	stop := context.AfterFunc(ctx, func() { l.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	// serial 让修改状态的请求逐个执行
	var serial sync.Mutex
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			// 瞬时错误:稍等重试
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer conn.Close()
			// ctx 取消时立刻打断进行中的读写
			cstop := context.AfterFunc(ctx, func() { conn.Close() })
			defer cstop()
			conn.SetDeadline(time.Now().Add(controlConnTimeout))
			handleControlConn(conn, s, onSetName, &serial)
		}()
	}
}

func handleControlConn(conn net.Conn, s *Share, onSetName func(string) error, serial *sync.Mutex) {
	var resp ControlResponse
	line, err := readControlLine(conn)
	if err != nil {
		resp = ControlResponse{Error: "读取请求失败:" + err.Error()}
	} else {
		var req ControlRequest
		if err := json.Unmarshal(line, &req); err != nil {
			resp = ControlResponse{Error: "请求格式错误:" + err.Error()}
		} else {
			serial.Lock()
			resp = applyControl(req, s, onSetName)
			serial.Unlock()
		}
	}
	data, _ := json.Marshal(resp)
	conn.Write(append(data, '\n'))
}

func readControlLine(conn net.Conn) ([]byte, error) {
	r := bufio.NewReaderSize(conn, controlMaxRequest)
	line, err := r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return nil, errors.New("请求过长")
	}
	if err != nil && len(line) == 0 {
		return nil, err
	}
	return line, nil
}

func applyControl(req ControlRequest, s *Share, onSetName func(string) error) ControlResponse {
	var err error
	switch req.Op {
	case "status":
	case "pause":
		err = s.Pause()
	case "resume":
		err = s.Resume()
	case "retry":
		s.Retry()
	case "set-name":
		if err = s.SetName(req.Name); err == nil && onSetName != nil {
			if perr := onSetName(req.Name); perr != nil {
				err = fmt.Errorf("名字已生效,但保存失败:%w", perr)
			}
		}
	default:
		return ControlResponse{Error: fmt.Sprintf("未知操作 %q", req.Op)}
	}
	st := s.Status()
	if err != nil {
		return ControlResponse{Error: err.Error(), Status: &st}
	}
	return ControlResponse{OK: true, Status: &st}
}

// ControlCall 向 addr 上的控制通道发送一次请求并返回响应,总超时 3 秒。
func ControlCall(addr string, req ControlRequest) (ControlResponse, error) {
	conn, err := dialControl(addr, controlCallTimeout)
	if err != nil {
		return ControlResponse{}, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(controlCallTimeout))
	data, _ := json.Marshal(req)
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return ControlResponse{}, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return ControlResponse{}, err
	}
	var resp ControlResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return ControlResponse{}, fmt.Errorf("控制通道响应无法解析:%w", err)
	}
	return resp, nil
}
