package tunnel

import (
	"fmt"

	"golang.org/x/sys/windows/registry"
)

// machineID 读取 HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid。
func machineID() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`,
		registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", fmt.Errorf("打开注册表失败: %w", err)
	}
	defer k.Close()
	v, _, err := k.GetStringValue("MachineGuid")
	if err != nil {
		return "", fmt.Errorf("读取 MachineGuid 失败: %w", err)
	}
	return v, nil
}

func osVersion() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`,
		registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue("ProductName")
	return v
}
