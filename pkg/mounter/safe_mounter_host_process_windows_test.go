//go:build windows
// +build windows

/*
Copyright 2026 The Kubernetes Authors.

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

package mounter

import (
	"fmt"
	"testing"

	"github.com/kubernetes-csi/csi-driver-smb/pkg/os/smb"
)

func TestCanonicalizeSMBRemotePath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "normalizes separators case and trailing slash",
			path: `//SERVER/Share/`,
			want: `\\server\share`,
		},
		{
			name: "preserves normalized unc path",
			path: `\\server\share`,
			want: `\\server\share`,
		},
		{
			name: "strips repeated trailing backslashes",
			path: `\\SERVER\Share\\\\`,
			want: `\\server\share`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := canonicalizeSMBRemotePath(test.path); got != test.want {
				t.Fatalf("canonicalizeSMBRemotePath(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}

func TestEnsureHostProcessSMBGlobalMapping(t *testing.T) {
	tests := []struct {
		name          string
		status        smb.SMBGlobalMappingStatus
		pathValid     bool
		pathValidErr  error
		statusErr     error
		removeErr     error
		newErr        error
		wantRemove    bool
		wantNew       bool
		wantPathValid bool
		wantErr       bool
	}{
		{
			name:          "ok and valid reuses mapping",
			status:        smb.SMBGlobalMappingStatusOK,
			pathValid:     true,
			wantPathValid: true,
		},
		{
			name:          "ok but invalid removes and recreates mapping",
			status:        smb.SMBGlobalMappingStatusOK,
			pathValid:     false,
			wantRemove:    true,
			wantNew:       true,
			wantPathValid: true,
		},
		{
			name:       "disconnected removes and recreates mapping",
			status:     smb.SMBGlobalMappingStatusDisconnected,
			wantRemove: true,
			wantNew:    true,
		},
		{
			name:       "other unhealthy state removes and recreates mapping",
			status:     smb.SMBGlobalMappingStatusOther,
			wantRemove: true,
			wantNew:    true,
		},
		{
			name:    "not found creates mapping",
			status:  smb.SMBGlobalMappingStatusNotFound,
			wantNew: true,
		},
		{
			name:      "status lookup error falls back to create",
			status:    smb.SMBGlobalMappingStatusNotFound,
			statusErr: fmt.Errorf("lookup failed"),
			wantNew:   true,
		},
		{
			name:          "path validation error still recreates when invalid",
			status:        smb.SMBGlobalMappingStatusOK,
			pathValid:     false,
			pathValidErr:  fmt.Errorf("validation failed"),
			wantPathValid: true,
			wantRemove:    true,
			wantNew:       true,
		},
		{
			name:       "remove failure is returned",
			status:     smb.SMBGlobalMappingStatusDisconnected,
			removeErr:  fmt.Errorf("remove failed"),
			wantRemove: true,
			wantErr:    true,
		},
		{
			name:    "new mapping failure is returned",
			status:  smb.SMBGlobalMappingStatusNotFound,
			newErr:  fmt.Errorf("new failed"),
			wantNew: true,
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var gotRemove, gotNew, gotPathValid bool
			err := ensureHostProcessSMBGlobalMapping(
				`\\server\share`,
				"user",
				"password",
				true,
				func(string) (smb.SMBGlobalMappingStatus, error) {
					return test.status, test.statusErr
				},
				func(string) (bool, error) {
					gotPathValid = true
					return test.pathValid, test.pathValidErr
				},
				func(string) error {
					gotRemove = true
					return test.removeErr
				},
				func(remotePath, username, password string, requirePrivacy bool) error {
					gotNew = true
					if remotePath != `\\server\share` || username != "user" || password != "password" || !requirePrivacy {
						t.Fatalf("unexpected NewSmbGlobalMapping args: %q %q %q %v", remotePath, username, password, requirePrivacy)
					}
					return test.newErr
				},
			)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, test.wantErr)
			}
			if gotRemove != test.wantRemove {
				t.Fatalf("remove called = %v, want %v", gotRemove, test.wantRemove)
			}
			if gotNew != test.wantNew {
				t.Fatalf("new called = %v, want %v", gotNew, test.wantNew)
			}
			if gotPathValid != test.wantPathValid {
				t.Fatalf("pathValid called = %v, want %v", gotPathValid, test.wantPathValid)
			}
		})
	}
}

func TestMappingRecoveryPreservesPrivacyAndBoundsRetries(t *testing.T) {
	conflict := fmt.Errorf("Multiple connections to a server or shared resource by the same user, using more than one user name, are not allowed")
	denied := fmt.Errorf("access denied")
	for _, privacy := range []bool{false, true} {
		for _, tc := range []struct {
			name                      string
			first, second, cleanupErr error
			wantCalls, wantCleanup    int
			wantErr                   error
		}{
			{"success", nil, nil, nil, 1, 0, nil},
			{"unrelated failure", denied, nil, nil, 1, 0, denied},
			{"recovery", conflict, nil, nil, 2, 1, nil},
			{"persistent conflict", conflict, conflict, nil, 2, 1, conflict},
			{"cleanup failure", conflict, denied, denied, 2, 1, denied},
		} {
			t.Run(fmt.Sprintf("%s/privacy=%t", tc.name, privacy), func(t *testing.T) {
				calls, cleanups := 0, 0
				err := newSMBMappingWithRecovery(`\\server\share`, "user", "password", privacy,
					func(path, user, password string, requirePrivacy bool) error {
						calls++
						if path != `\\server\share` || user != "user" || password != "password" || requirePrivacy != privacy {
							t.Fatal("recovery changed mapping arguments or encryption policy")
						}
						if calls == 1 {
							return tc.first
						}
						return tc.second
					}, func(path string) error {
						cleanups++
						if path != `\\server\share` {
							t.Fatal("cleanup changed server path")
						}
						return tc.cleanupErr
					})
				if err != tc.wantErr || calls != tc.wantCalls || cleanups != tc.wantCleanup {
					t.Fatalf("got error=%v calls=%d cleanups=%d; want error=%v calls=%d cleanups=%d", err, calls, cleanups, tc.wantErr, tc.wantCalls, tc.wantCleanup)
				}
			})
		}
	}
}
