package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"

	"github.com/rancher/go-rancher/v2"
	"github.com/urfave/cli"
)

func SSHCommand() cli.Command {
	return cli.Command{
		Name:        "ssh",
		Usage:       "SSH into host",
		Description: "\nFor any hosts created through Rancher using docker-machine, you can SSH into the host. This is not supported for any custom hosts. If the host is not in the current $RANCHER_ENVIRONMENT, use `--env <envID>` or `--env <envName>` to select a different environment.\n\nExample:\n\t$ rancher ssh 1h1\n\t$ rancher --env 1a5 ssh 1h5\n",
		ArgsUsage:   "[HOSTID HOSTNAME...]",
		Action:      hostSSH,
		Flags:       []cli.Flag{},
	}
}

func hostSSH(ctx *cli.Context) error {
	c, err := GetClient(ctx)
	if err != nil {
		return err
	}

	hostname := ""
	args := ctx.Args()

	for _, arg := range args {
		if len(arg) > 0 && arg[0] != '-' {
			parts := strings.SplitN(arg, "@", 2)
			hostname = parts[len(parts)-1]
			break
		}
	}

	if hostname == "" {
		return fmt.Errorf("Failed to find hostname in %v", args)
	}

	host, err := Lookup(c, hostname, "host")
	if err != nil {
		return err
	}

	var physicalHost client.PhysicalHost
	err = c.GetLink(*host, "physicalHost", &physicalHost)
	if err != nil {
		return err
	}

	if physicalHost.Type != "machine" {
		return fmt.Errorf("Can only SSH to docker-machine created hosts.  No custom hosts")
	}

	key, err := getSSHKey(hostname, physicalHost)
	if err != nil {
		return err
	}

	ips := client.IpAddressCollection{}
	if err := c.GetLink(*host, "ipAddresses", &ips); err != nil {
		return err
	}

	if len(ips.Data) == 0 {
		return fmt.Errorf("Failed to find IP for %s", hostname)
	}

	return processExitCode(callSSH(key, ips.Data[0].Address, ctx.Args()))
}

func callSSH(content []byte, ip string, args []string) error {
	for i, val := range args {
		if !strings.HasPrefix(val, "-") && len(val) > 0 {
			parts := strings.SplitN(val, "@", 2)
			parts[len(parts)-1] = ip
			args[i] = strings.Join(parts, "@")
		}
	}

	tmpfile, err := ioutil.TempFile("", "ssh")
	if err != nil {
		return err
	}
	defer os.Remove(tmpfile.Name())

	if err := os.Chmod(tmpfile.Name(), 0600); err != nil {
		return err
	}

	_, err = tmpfile.Write(content)
	if err != nil {
		return err
	}

	if err := tmpfile.Close(); err != nil {
		return err
	}

	cmd := exec.Command("ssh", append([]string{"-i", tmpfile.Name()}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func getSSHKey(hostname string, physicalHost client.PhysicalHost) ([]byte, error) {
	link, ok := physicalHost.Links["config"]
	if !ok {
		return nil, fmt.Errorf("Failed to find SSH key for %s", hostname)
	}

	resp, err := http.Get(link)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	tarGz, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s", tarGz)
	}

	gzipIn, err := gzip.NewReader(bytes.NewBuffer(tarGz))
	if err != nil {
		return nil, err
	}
	tar := tar.NewReader(gzipIn)

	for {
		header, err := tar.Next()
		if err != nil {
			return nil, err
		}

		if path.Base(header.Name) == "id_rsa" {
			return ioutil.ReadAll(tar)
		}
	}
}
