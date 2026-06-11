//go:build windows
// +build windows

/*
Copyright 2023 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package smb

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kubernetes-csi/csi-driver-smb/pkg/util"
	"k8s.io/klog/v2"
)

func IsSmbMapped(remotePath string) (bool, error) {
	cmdLine := `$(Get-SmbGlobalMapping -RemotePath $Env:smbremotepath -ErrorAction Stop).Status`
	cmdEnv := fmt.Sprintf("smbremotepath=%s", remotePath)
	out, err := util.RunPowershellCmd(cmdLine, cmdEnv)
	if err != nil {
		return false, fmt.Errorf("error checking smb mapping. cmd %s, output: %s, err: %v", remotePath, string(out), err)
	}

	if len(out) == 0 || !strings.EqualFold(strings.TrimSpace(string(out)), "OK") {
		return false, nil
	}
	return true, nil
}

func NewSmbGlobalMapping(remotePath, username, password string) error {
	// use PowerShell Environment Variables to store user input string to prevent command line injection
	// https://docs.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_environment_variables?view=powershell-5.1
	cmdLine := fmt.Sprintf(`$PWord = ConvertTo-SecureString -String $Env:smbpassword -AsPlainText -Force` +
		`;$Credential = New-Object -TypeName System.Management.Automation.PSCredential -ArgumentList $Env:smbuser, $PWord` +
		`;New-SmbGlobalMapping -RemotePath $Env:smbremotepath -Credential $Credential -RequirePrivacy $true`)

	klog.V(2).Infof("begin to run NewSmbGlobalMapping with %s, %s", remotePath, username)
	if output, err := util.RunPowershellCmd(cmdLine, fmt.Sprintf("smbuser=%s", username),
		fmt.Sprintf("smbpassword=%s", password),
		fmt.Sprintf("smbremotepath=%s", remotePath)); err != nil {
		return fmt.Errorf("NewSmbGlobalMapping failed. output: %q, err: %v", string(output), err)
	}
	return nil
}

func RemoveSmbGlobalMapping(remotePath string) error {
	remotePath = strings.TrimSuffix(remotePath, `\`)
	cmd := `Remove-SmbGlobalMapping -RemotePath $Env:smbremotepath -Force`
	klog.V(2).Infof("begin to run RemoveSmbGlobalMapping with %s", remotePath)
	if output, err := util.RunPowershellCmd(cmd, fmt.Sprintf("smbremotepath=%s", remotePath)); err != nil {
		return fmt.Errorf("UnmountSmbShare failed. output: %q, err: %v", string(output), err)
	}
	return nil
}

// IsCredentialSessionConflict reports whether an error from
// New-SmbGlobalMapping is ERROR_SESSION_CREDENTIAL_CONFLICT (1219): "Multiple
// connections to a server or shared resource by the same user, using more
// than one user name, are not allowed". Dead mappings that survived a node
// reboot can hold defunct sessions to the server and poison new mappings
// with this error (kubernetes-csi/csi-driver-smb#1007).
func IsCredentialSessionConflict(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Multiple connections to a server or shared resource")
}

// ListSmbGlobalMappingRemotePaths returns the RemotePath of every existing
// SMB global mapping on the node.
func ListSmbGlobalMappingRemotePaths() ([]string, error) {
	cmd := `(Get-SmbGlobalMapping -ErrorAction SilentlyContinue).RemotePath`
	out, err := util.RunPowershellCmd(cmd)
	if err != nil {
		return nil, fmt.Errorf("error listing smb mappings. output: %q, err: %v", string(out), err)
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}

// SelectStaleMappingsToServer returns the subset of mappings that point at
// the same \\server as remotePath and are no longer accessible per pathValid.
// Pure selection logic, kept separate from the powershell calls for testing.
func SelectStaleMappingsToServer(remotePath string, mappings []string, pathValid func(string) (bool, error)) ([]string, error) {
	trimmed := strings.TrimPrefix(strings.ReplaceAll(remotePath, "/", `\`), `\\`)
	server := strings.SplitN(trimmed, `\`, 2)[0]
	if server == "" {
		return nil, fmt.Errorf("could not derive server from remote path %q", remotePath)
	}
	serverPrefix := strings.ToLower(`\\` + server + `\`)

	var stale []string
	for _, path := range mappings {
		if !strings.HasPrefix(strings.ToLower(path), serverPrefix) {
			continue
		}
		if valid, err := pathValid(path); err == nil && valid {
			continue
		}
		stale = append(stale, path)
	}
	return stale, nil
}

// RemoveStaleMappingsToServer removes every SMB global mapping to the same
// \\server as remotePath whose remote path is no longer accessible. Used to
// recover from ERROR_SESSION_CREDENTIAL_CONFLICT, where mappings with dead
// credentials block new mappings to the same server.
func RemoveStaleMappingsToServer(remotePath string, pathValid func(string) (bool, error)) error {
	paths, err := ListSmbGlobalMappingRemotePaths()
	if err != nil {
		return err
	}
	stale, err := SelectStaleMappingsToServer(remotePath, paths, pathValid)
	if err != nil {
		return err
	}
	var errs []string
	for _, path := range stale {
		klog.Warningf("removing stale SMB global mapping %s (same server as %s, no longer accessible)", path, remotePath)
		if err := RemoveSmbGlobalMapping(path); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed to remove stale mappings: %s", strings.Join(errs, "; "))
	}
	return nil
}

// GetRemoteServerFromTarget- gets the remote server path given a mount point, the function is recursive until it find the remote server or errors out
func GetRemoteServerFromTarget(mount string) (string, error) {
	target, err := os.Readlink(mount)
	klog.V(2).Infof("read link for mount %s, target: %s", mount, target)
	if err != nil || len(target) == 0 {
		return "", fmt.Errorf("error reading link for mount %s. target %s err: %v", mount, target, err)
	}
	return strings.TrimSpace(target), nil
}

// CheckForDuplicateSMBMounts checks if there is any other SMB mount exists on the same remote server
func CheckForDuplicateSMBMounts(dir, mount, remoteServer string) (bool, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}

	for _, file := range files {
		klog.V(6).Infof("checking file %s", file.Name())
		if file.IsDir() {
			globalMountPath := filepath.Join(dir, file.Name(), "globalmount")
			if strings.EqualFold(filepath.Clean(globalMountPath), filepath.Clean(mount)) {
				klog.V(2).Infof("skip current mount path %s", mount)
			} else {
				fileInfo, err := os.Lstat(globalMountPath)
				// check if the file is a symlink, if yes, check if it is pointing to the same remote server
				if err == nil && fileInfo.Mode()&os.ModeSymlink != 0 {
					remoteServerPath, err := GetRemoteServerFromTarget(globalMountPath)
					klog.V(2).Infof("checking remote server path %s on local path %s", remoteServerPath, globalMountPath)
					if err == nil {
						if remoteServerPath == remoteServer {
							return true, nil
						}
					} else {
						klog.Errorf("GetRemoteServerFromTarget(%s) failed with %v", globalMountPath, err)
					}
				}
			}
		}
	}
	return false, err
}
