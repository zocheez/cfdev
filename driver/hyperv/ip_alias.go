package hyperv

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func (d *HyperV) RemoveLoopbackAliases(switchName string, addrs ...string) error {
	exists, err := d.switchExists(switchName)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	_, err = d.Powershell.Output(fmt.Sprintf("Remove-VMSwitch -Name %s -force", switchName))
	return err
}

func (d *HyperV) AddLoopbackAliases(switchName string, addrs ...string) error {
	fmt.Println("Setting up IP aliases for the BOSH Director & CF Router (requires administrator privileges)")

	if err := d.createSwitchIfNotExist(switchName); err != nil {
		return err
	}

	for _, addr := range addrs {
		exists, err := d.aliasExists(addr)

		if err != nil {
			return err
		}

		if exists {
			continue
		}

		err = d.addAlias(switchName, addr)
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *HyperV) loopback(switchName string) string {
	return fmt.Sprintf("vEthernet (%s)", switchName)
}


func (d *HyperV) addAlias(switchName, alias string) error {
	cmd := exec.Command("netsh", "interface", "ip", "add", "address", d.loopback(switchName), alias, "255.255.255.255")

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to add network alias: %s, %s, %s, %s", d.loopback(switchName), alias, err, output)
	}

	return d.waitForAlias(alias)
}

func (d *HyperV) aliasExists(alias string) (bool, error) {
	output, err := d.Powershell.Output("ipconfig")
	if err != nil {
		return false, err
	}

	return strings.Contains(output, alias), nil
}

func (d *HyperV) createSwitchIfNotExist(switchName string) error {
	exists, err := d.switchExists(switchName)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	_, err = d.Powershell.Output(fmt.Sprintf("New-VMSwitch -Name %s -SwitchType Internal -Notes 'Switch for CF Dev Networking'", switchName))
	return err
}

func (d *HyperV) switchExists(switchName string) (bool, error) {
	output, err := d.Powershell.Output(fmt.Sprintf("Get-VMSwitch %s*", switchName))
	if err != nil {
		return false, err
	} else if output == "" {
		return false, nil
	}

	return true, nil
}

// TODO: make prettier
func (d *HyperV) waitForAlias(addr string) error {
	done := make(chan error)
	go func() {
		for {
			if exists, err := d.aliasExists(addr); !exists {
				time.Sleep(3 * time.Second)
			} else if err != nil {
				done <- err
				close(done)
				return
			} else {
				close(done)
				return
			}
		}
	}()

	select {
	case err := <-done:
		return err
	case _ = <-time.After(1 * time.Minute):
		return fmt.Errorf("timed out waiting for alias %s", addr)
	}
}

