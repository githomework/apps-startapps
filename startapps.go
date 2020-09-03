package main

import (
	"fmt"
	//	"fmt"
	"flag"
	"io/ioutil"
	"log"
	"net/http"
	"os"


	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
"github.com/gitdlam/apps-app"
	"github.com/BurntSushi/toml"

	"github.com/kardianos/service"
	"github.com/mitchellh/go-ps"
)

var logger service.Logger

type program struct{}

func (p *program) Start(s service.Service) error {
	// Start should not block. Do the actual work async.
	go p.run()
	return nil
}
func (p *program) run() {
	doWork()
}
func (p *program) Stop(s service.Service) error {
	// Stop should not block. Return with a few seconds.
	stopWork()
	return nil
}

type global_entry struct {
	Exe     string `json:"exe"`
	Port    string `json:"port"`
	Pg_code string `json:"pg_code"`
	Args    string
	User    string
}

type app_entry struct {
	sync.Mutex
	Exe         string `json:"exe"`
	AutoRestart bool   `toml:"auto_restart"`
	Folder      string `json:"folder"`
	Port        string `json:"port"`
	Args        string `json:"args"`
	RunAsUser   bool   `toml:"run_as_user"`
}

type tomlConfig struct {
	sync.RWMutex
	ServePort string `toml:"serve_port"  json:"serve_port"`
	PgCode    string `toml:"pg_code"     json:"pg_code"`
	User      string
	UserCode  string
	Global    []global_entry
	Apps      []app_entry
}

type appType struct {
	exe       string
	args      string
	path      string
	folder    string
	runAsUser bool
}

type globalType struct {
	app.AppType
	config tomlConfig
	//	route_ports map[string]map[string]string
	appMap map[string]*appType
	logSrv service.Logger
}

var global globalType

func ping(port string, expected_reply string) bool {

	timeout := time.Duration(time.Second)
	client := http.Client{
		Timeout: timeout,
	}
	if port[0:1] == ":" {
		port = port[1:]
	}
	response, err := client.Get("http://localhost:" + port + "/ping")
	if err != nil {
		log.Printf("No reply from %s. Starting...\n", expected_reply)
		return false
	} else {
		defer response.Body.Close()
		contents, err := ioutil.ReadAll(response.Body)
		if err != nil {
			log.Printf("%s", err)
			return false
		}
		reply := string(contents)
		if reply == expected_reply {
			//        log.Printf("%s%s is alive.", expected_reply, port)
		} else {
			log.Printf("Unexpected executable %s using port %s", reply, port)
		}
		return true
	}
}

func configServices() {
	global.AppType.Setup()

	if _, err := toml.DecodeFile(global.Folder+"/"+global.Name+".toml", &global.config); err != nil {
		log.Printf("%s", err)
		return
	}

	global.appMap = map[string]*appType{}

	for _, v := range global.config.Global {
		if v.Exe == global.Name {
			global.config.ServePort = v.Port
			global.config.PgCode = v.Pg_code
			cred := strings.Split(DecryptString(v.User), "::")
			global.config.User = cred[0]
			global.config.UserCode = cred[1]
			//os.Setenv("SPAUTH_USERNAME", strings.ToLower(cred[0]+"@winc.com.au"))
			//log.Println(global.config.User, global.config.UserCode)
		}
		//global.appMap[v.Exe] = &appType{exe: v.Exe, args: v.Args}
	}

	for _, v := range global.config.Apps {
		if v.AutoRestart && v.Exe != global.Name {
			global.appMap[v.Exe] = &appType{exe: strings.ToLower(v.Exe), args: v.Args, path: v.Folder + "/" + v.Exe, folder: v.Folder, runAsUser: v.RunAsUser}
		}
	}

}

func checkServices() int {
	processes := currentProcesses()
	var i int
	global.config.RLock()
	fmt.Print("\nChecking ")
	for _, app := range global.appMap {
		fmt.Print(".")
		if !processes[app.exe] {
			i++
			fmt.Println("\nStarting ", app.exe)

			cmd := exec.Command(app.path, app.args)
			if runtime.GOOS == "windows" {
				//				cmd = exec.Command(exe_map["exe"] + ".exe", exe_map["args"])
				flds := strings.Fields(app.args)
				if app.exe == "g04_cmd" {
					flds = append(flds, "--spuser="+strings.ToLower(global.config.User)+"@winc.com.au", "--sppass="+global.config.UserCode)
				}
				if app.runAsUser {
					flds = append([]string{"-accepteula", "-u", "ce\\" + global.config.User, "-p", global.config.UserCode, "-h", "-i", app.path + ".exe"}, flds...)
					//log.Println(flds)
					cmd = exec.Command(global.Folder+"/PsExec.exe", flds...)
					// environment not being passed on to app.  Suspect psexec isn't passing it on.
					cmd.Env = append(os.Environ(), "spuser="+strings.ToLower(global.config.User)+"@winc.com.au", "sppass="+global.config.UserCode)
				} else {
					cmd = exec.Command(app.path+".exe", flds...)

				}
			}
			cmd.Dir = app.folder

			cmd.Start()
			//cmd.Wait()
			// global.route_ports[port].Mutex.Unlock()

			time.Sleep(time.Duration(100) * time.Millisecond)
		}

	}
	global.config.RUnlock()
	return i
}

func main() {
	log.SetFlags(log.Lshortfile)
	svcFlag := flag.String("service", "", "Control the system service.")
	flag.Parse()
	svcConfig := &service.Config{
		Name:        "startApps",
		DisplayName: "Start Apps",
		Description: "Start and restart apps",
	}

	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		log.Fatal(err)
	}
	logger, err = s.Logger(nil)
	if err != nil {
		log.Fatal(err)
	}
	if len(*svcFlag) != 0 {
		err := service.Control(s, *svcFlag)
		if err != nil {
			log.Printf("Valid actions: %q\n", service.ControlAction)
			log.Fatal(err)
		}
		return
	}
	err = s.Run()
	if err != nil {
		logger.Error(err)
	}
}

func doWork() {
	configServices()
	ticker := time.NewTicker(4 * time.Second)
	quit := make(chan struct{})

	go func() {
		for {
			select {
			case <-ticker.C:
				checkServices()
				//				time.Sleep(time.Duration(i*500) * time.Millisecond)

			case <-quit:
				ticker.Stop()
				return
			}
		}
	}()
}

func stopWork() {

}

func processExists(p string) bool {
	output, _ := exec.Command("powershell", "Get-Process", "-name", p, "|", "select", "-exp", "name").Output()
	if string(output) == "" {
		return false
	}
	return strings.ToLower(strings.Fields(string(output))[0]) == strings.ToLower(p)

}

func currentProcesses() map[string]bool {
	m := map[string]bool{}
	processes, err := ps.Processes()
	if err != nil {
		log.Println(err)
		return m
	}
	for _, v := range processes {
		exe := v.Executable()
		if exe[0] != 'g' {
			continue
		}
		m[strings.Split(exe, ".exe")[0]] = true
	}
	return m
}

func currentProcessesOld() map[string]bool {
	output, _ := exec.Command("powershell", "Get-Process", "-name", "g*", "|", "select", "-exp", "name").Output()
	apps := strings.Fields(strings.ToLower(string(output)))
	m := map[string]bool{}
	for _, v := range apps {
		m[v] = true
	}
	return m

}
