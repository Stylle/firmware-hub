// update-agent is a local desktop helper. It has no network or server login.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/Stylle/firmware-hub/internal/updater"
	"io"
	"os"
	"path/filepath"
	"time"
)

func main() {
	plan := flag.String("plan", "", "local installation plan JSON")
	rollback := flag.String("rollback", "", "restore latest backup in an absolute application directory")
	pid := flag.Int("wait-pid", 0, "wait up to 120 seconds for the main application to exit; never terminate it")
	flag.Parse()
	if *pid < 0 || (*plan == "") == (*rollback == "") {
		fmt.Fprintln(os.Stderr, "choose --plan FILE or --rollback APP_DIRECTORY")
		os.Exit(2)
	}
	e := perform(*plan, *rollback, *pid)
	if *plan != "" {
		result := map[string]any{"success": e == nil, "at": time.Now().UTC().Format(time.RFC3339)}
		if e != nil {
			result["error"] = e.Error()
		}
		b, _ := json.MarshalIndent(result, "", "  ")
		f, err := os.CreateTemp(filepath.Dir(*plan), ".update-result-*")
		if err == nil {
			_, err = f.Write(b)
			if err == nil {
				err = f.Sync()
			}
			ce := f.Close()
			if err == nil {
				err = ce
			}
			if err == nil {
				err = os.Rename(f.Name(), *plan+".result.json")
			}
			if err != nil {
				_ = os.Remove(f.Name())
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "could not save result:", err)
		}
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	fmt.Println("Update operation complete. Original files retained after installation; no package command executed.")
}
func perform(plan, rollback string, pid int) error {
	if e := waitProcess(pid, 120*time.Second); e != nil {
		return e
	}
	var e error
	if rollback != "" {
		e = updater.Rollback(rollback)
	} else {
		f, err := os.Open(plan)
		if err != nil {
			return err
		}
		var p updater.Plan
		d := json.NewDecoder(io.LimitReader(f, 16385))
		d.DisallowUnknownFields()
		e = d.Decode(&p)
		if e == nil {
			var extra any
			if d.Decode(&extra) != io.EOF {
				e = fmt.Errorf("trailing plan data")
			}
		}
		f.Close()
		if e == nil {
			e = updater.Apply(p)
		}
	}
	return e
}
