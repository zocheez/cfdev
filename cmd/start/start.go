package start

import (
	"io"
	"time"

	"code.cloudfoundry.org/cfdev/metadata"

	"code.cloudfoundry.org/cfdev/config"
	e "code.cloudfoundry.org/cfdev/errors"
	"code.cloudfoundry.org/cfdev/provision"
	"code.cloudfoundry.org/cfdev/resource"
	"fmt"
	"github.com/spf13/cobra"
	"os"
	"strings"

	"code.cloudfoundry.org/cfdev/cfanalytics"
	"code.cloudfoundry.org/cfdev/hypervisor"
	"path/filepath"
)

//go:generate mockgen -package mocks -destination mocks/ui.go code.cloudfoundry.org/cfdev/cmd/start UI
type UI interface {
	Say(message string, args ...interface{})
	Writer() io.Writer
}

//go:generate mockgen -package mocks -destination mocks/analytics_client.go code.cloudfoundry.org/cfdev/cmd/start AnalyticsClient
type AnalyticsClient interface {
	Event(event string, data ...map[string]interface{}) error
	PromptOptInIfNeeded(message string) error
}

//go:generate mockgen -package mocks -destination mocks/toggle.go code.cloudfoundry.org/cfdev/cmd/start Toggle
type Toggle interface {
	Enabled() bool
	SetCFAnalyticsEnabled(value bool) error
	SetCustomAnalyticsEnabled(value bool) error
	SetProp(k, v string) error
}

//go:generate mockgen -package mocks -destination mocks/system-profiler.go code.cloudfoundry.org/cfdev/cmd/start SystemProfiler
type SystemProfiler interface {
	GetAvailableMemory() (uint64, error)
	GetTotalMemory() (uint64, error)
}

//go:generate mockgen -package mocks -destination mocks/network.go code.cloudfoundry.org/cfdev/cmd/start HostNet
type HostNet interface {
	AddLoopbackAliases(...string) error
}

//go:generate mockgen -package mocks -destination mocks/host.go code.cloudfoundry.org/cfdev/cmd/start Host
type Host interface {
	CheckRequirements() error
}

//go:generate mockgen -package mocks -destination mocks/cache.go code.cloudfoundry.org/cfdev/cmd/start Cache
type Cache interface {
	Sync(resource.Catalog) error
}

//go:generate mockgen -package mocks -destination mocks/cfdevd.go code.cloudfoundry.org/cfdev/cmd/start CFDevD
type CFDevD interface {
	Install() error
}

//go:generate mockgen -package mocks -destination mocks/driver.go code.cloudfoundry.org/cfdev/cmd/start Driver
type Driver interface {
	Start(cpus int, memory int, efiPath string) error
	Stop() error
	IsRunning() (bool, error)
}

//go:generate mockgen -package mocks -destination mocks/vpnkit.go code.cloudfoundry.org/cfdev/cmd/start VpnKit
type VpnKit interface {
	Start() error
	Stop() error
	Watch(chan string)
}

//go:generate mockgen -package mocks -destination mocks/analyticsd.go code.cloudfoundry.org/cfdev/cmd/start AnalyticsD
type AnalyticsD interface {
	Start() error
	Stop() error
	IsRunning() (bool, error)
}

//go:generate mockgen -package mocks -destination mocks/hypervisor.go code.cloudfoundry.org/cfdev/cmd/start Hypervisor
type Hypervisor interface {
	CreateVM(vm hypervisor.VM) error
	Start(vmName string) error
	Stop(vmName string) error
	IsRunning(vmName string) (bool, error)
}

//go:generate mockgen -package mocks -destination mocks/provisioner.go code.cloudfoundry.org/cfdev/cmd/start Provisioner
type Provisioner interface {
	Ping(duration time.Duration) error
}

//go:generate mockgen -package mocks -destination mocks/provision.go code.cloudfoundry.org/cfdev/cmd/start Provision
type Provision interface {
	Execute(args Args) error
}

//go:generate mockgen -package mocks -destination mocks/isoreader.go code.cloudfoundry.org/cfdev/cmd/start MetaDataReader
type MetaDataReader interface {
	Read(tarballPath string) (metadata.Metadata, error)
}

//go:generate mockgen -package mocks -destination mocks/stop.go code.cloudfoundry.org/cfdev/cmd/start Stop
type Stop interface {
	RunE(cmd *cobra.Command, args []string) error
}

//go:generate mockgen -package mocks -destination mocks/env.go code.cloudfoundry.org/cfdev/cmd/start Env
type Env interface {
	CreateDirs() error
	SetupState(depsFile string) error
}

type Args struct {
	Registries          string
	DeploySingleService string
	DepsPath            string
	EFIPath             string
	NoProvision         bool
	Cpus                int
	Mem                 int
}

type Start struct {
	Exit            chan struct{}
	LocalExit       chan string
	UI              UI
	Config          config.Config
	MetaDataReader  MetaDataReader
	Analytics       AnalyticsClient
	AnalyticsToggle Toggle
	HostNet         HostNet
	Host            Host
	Cache           Cache
	CFDevD          CFDevD
	VpnKit          VpnKit
	AnalyticsD      AnalyticsD
	Hypervisor      Hypervisor
	Driver          Driver
	Stop            Stop
	Provisioner     Provisioner
	Provision       Provision
	Env             Env
	Profiler        SystemProfiler
}

const (
	compatibilityVersion = "v4"
	defaultMemory        = 4192
)

func (s *Start) Cmd() *cobra.Command {
	args := Args{}
	cmd := &cobra.Command{
		Use: "start",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := s.Execute(args); err != nil {
				return e.SafeWrap(err, "cf dev start")
			}
			return nil
		},
	}

	pf := cmd.PersistentFlags()
	pf.StringVarP(&args.DepsPath, "file", "f", "", "path to .dev file containing bosh & cf bits")
	pf.StringVarP(&args.Registries, "registries", "r", "", "docker registries that skip ssl validation - ie. host:port,host2:port2")
	pf.IntVarP(&args.Cpus, "cpus", "c", 4, "cpus to allocate to vm")
	pf.IntVarP(&args.Mem, "memory", "m", 0, "memory to allocate to vm in MB")
	pf.BoolVarP(&args.NoProvision, "no-provision", "n", false, "start vm but do not provision")
	pf.StringVarP(&args.DeploySingleService, "white-listed-services", "s", "", "list of supported services to deploy")
	pf.StringVarP(&args.EFIPath, "efi", "e", filepath.Join(s.Config.BinaryDir, "cfdev-efi-v2.iso"), "path to efi boot iso")

	pf.MarkHidden("no-provision")
	pf.MarkHidden("efi")
	return cmd
}

func (s *Start) Execute(args Args) error {
	go func() {
		select {
		case <-s.Exit:
			// no-op
		case name := <-s.LocalExit:
			s.UI.Say("ERROR: %s has stopped", name)
		}
		s.Driver.Stop()
		os.Exit(128)
	}()

	depsPath := filepath.Join(s.Config.CacheDir, "cfdev-deps.tgz")

	if args.DepsPath != "" {
		var err error
		depsPath, err = filepath.Abs(args.DepsPath)
		if err != nil {
			return e.SafeWrap(err, "determining absolute path to deps iso")
		}

		if _, err := os.Stat(depsPath); os.IsNotExist(err) {
			return fmt.Errorf("no file found at: %s", depsPath)
		}

		s.Config.Dependencies.Remove("cfdev-deps.tgz")
	}

	aMem, err := s.Profiler.GetAvailableMemory()
	if err != nil {
		fmt.Printf("AVAILABLE MEMORY ERROR: %v", err)
	}

	tMem, err := s.Profiler.GetTotalMemory()
	if err != nil {
		fmt.Printf("TOTAL MEMORY ERROR: %v", err)
	}

	if err := s.Host.CheckRequirements(); err != nil {
		return err
	}

	if running, err := s.Driver.IsRunning(); err != nil {
		return e.SafeWrap(err, "is running")
	} else if running {
		s.UI.Say("CF Dev is already running...")
		s.Analytics.Event(cfanalytics.START_END, map[string]interface{}{"alreadyrunning": true})
		return nil
	}

	if err := s.Stop.RunE(nil, nil); err != nil {
		return e.SafeWrap(err, "stopping cfdev")
	}

	if err := s.Env.CreateDirs(); err != nil {
		return e.SafeWrap(err, "setting up cfdev home dir")
	}

	if cfdevd := s.Config.Dependencies.Lookup("cfdevd"); cfdevd != nil {
		s.UI.Say("Downloading Network Helper...")

		if err := s.Cache.Sync(resource.Catalog{
			Items: []resource.Item{*cfdevd},
		}); err != nil {
			return e.SafeWrap(err, "Unable to download network helper")
		}

		s.Config.Dependencies.Remove("cfdevd")
	}

	// the two invocations below are "privileged actions"
	if err := s.osSpecificSetup(); err != nil {
		return err
	}

	if err := s.HostNet.AddLoopbackAliases(s.Config.BoshDirectorIP, s.Config.CFRouterIP); err != nil {
		return e.SafeWrap(err, "adding aliases")
	}

	s.UI.Say("Downloading Resources...")
	if err := s.Cache.Sync(s.Config.Dependencies); err != nil {
		return e.SafeWrap(err, "Unable to sync assets")
	}

	s.UI.Say("Setting State...")
	if err := s.Env.SetupState(depsPath); err != nil {
		return e.SafeWrap(err, "Unable to setup directories")
	}

	metaData, err := s.MetaDataReader.Read(filepath.Join(s.Config.StateDir, "metadata.yml"))
	if err != nil {
		return e.SafeWrap(err, fmt.Sprintf("%s is not compatible with CF Dev. Please use a compatible file.", depsPath))
	}

	s.AnalyticsToggle.SetProp("type", metaData.DeploymentName)
	s.AnalyticsToggle.SetProp("artifact", metaData.ArtifactVersion)

	if metaData.Version != compatibilityVersion {
		return fmt.Errorf("%s is not compatible with CF Dev. Please use a compatible file", depsPath)
	}

	s.Analytics.PromptOptInIfNeeded(metaData.AnalyticsMessage)

	s.Analytics.Event(cfanalytics.START_BEGIN, map[string]interface{}{
		"total memory":     tMem,
		"available memory": aMem,
	})

	if args.DeploySingleService != "" {
		if !s.isServiceSupported(args.DeploySingleService, metaData.Services) {
			return e.SafeWrap(err, fmt.Sprintf("Service: '%v' is not supported", args.DeploySingleService))
		}
		s.Analytics.Event(cfanalytics.SELECTED_SERVICE, map[string]interface{}{"services_requested": args.DeploySingleService})
	}

	memoryToAllocate, err := s.allocateMemory(metaData, args.Mem)
	if err != nil {
		return err
	}

	err = s.Driver.Start(args.Cpus, memoryToAllocate, args.EFIPath)
	if err != nil {
		return err
	}

	s.UI.Say("Waiting for the VM...")
	err = s.Provisioner.Ping(2 * time.Minute)
	if err != nil {
		return e.SafeWrap(err, "Timed out waiting for the VM")
	}

	if args.NoProvision {
		s.UI.Say("VM will not be provisioned because '-n' (no-provision) flag was specified.")
		return nil
	}

	if err := s.Provision.Execute(args); err != nil {
		return err
	}

	if s.AnalyticsToggle.Enabled() {
		err = s.AnalyticsD.Start()
	}

	s.Analytics.Event(cfanalytics.START_END)
	return nil
}

func (s *Start) isServiceSupported(service string, services []provision.Service) bool {
	if strings.ToLower(service) == "all" || strings.ToLower(service) == "none" {
		return true
	}

	for _, s := range strings.Split(service, ",") {
		if !contains(services, s) {
			return false
		}
	}

	return true
}

func contains(services []provision.Service, service string) bool {
	for _, s := range services {
		if strings.ToLower(s.Flagname) == strings.ToLower(service) {
			return true
		}
	}

	return false
}

func (s *Start) allocateMemory(metaData metadata.Metadata, requestedMem int) (int, error) {
	baseMem := defaultMemory
	if metaData.DefaultMemory > 0 {
		baseMem = metaData.DefaultMemory
	}

	availableMem, err := s.Profiler.GetAvailableMemory()
	if err != nil {
		return 0, e.SafeWrap(err, "error retrieving available system memory")
	}

	customMemProvided := requestedMem > 0
	if customMemProvided {
		if requestedMem >= baseMem {
			if availableMem >= uint64(requestedMem) {
				return requestedMem, nil
			}

			if availableMem < uint64(requestedMem) {
				s.UI.Say("WARNING: This machine may not have enough available RAM to run with what is specified.")
				return requestedMem, nil
			}
		}

		if requestedMem < baseMem {
			s.UI.Say(fmt.Sprintf("WARNING: It is recommended that you run %s Dev with at least %v MB of RAM.", strings.ToUpper(metaData.DeploymentName), baseMem))
			if availableMem >= uint64(requestedMem) {
				return requestedMem, nil
			}

			if availableMem < uint64(requestedMem) {
				s.UI.Say("WARNING: This machine may not have enough available RAM to run with what is specified.")
				return requestedMem, nil
			}
		}
	} else {
		if availableMem >= uint64(baseMem) {
			return baseMem, nil
		} else {
			s.UI.Say(fmt.Sprintf("WARNING: %s Dev requires %v MB of RAM to run. This machine may not have enough free RAM.", strings.ToUpper(metaData.DeploymentName), baseMem))
			return baseMem, nil
		}
	}

	return 0, nil
}
