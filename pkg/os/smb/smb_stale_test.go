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

package smb

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestIsCredentialSessionConflict(t *testing.T) {
	conflictErr := fmt.Errorf("NewSmbGlobalMapping failed. output: %q, err: %v",
		"New-SmbGlobalMapping : Multiple connections to a server or shared resource by the same user, using more than one user name, are not allowed.",
		errors.New("exit status 1"))
	tests := []struct {
		desc     string
		err      error
		expected bool
	}{
		{desc: "nil error", err: nil, expected: false},
		{desc: "unrelated error", err: errors.New("The user name or password is incorrect"), expected: false},
		{desc: "session credential conflict", err: conflictErr, expected: true},
	}
	for _, test := range tests {
		if got := IsCredentialSessionConflict(test.err); got != test.expected {
			t.Errorf("%s: got %v, expected %v", test.desc, got, test.expected)
		}
	}
}

func TestSelectStaleMappingsToServer(t *testing.T) {
	mappings := []string{
		`\\server-a\share\pvc-dead`,
		`\\server-a\share\pvc-alive`,
		`\\server-a\share\pvc-probe-error`,
		`\\SERVER-A\share\pvc-dead-mixed-case`,
		`\\server-b\share\pvc-other-server-dead`,
	}
	pathValid := func(path string) (bool, error) {
		switch path {
		case `\\server-a\share\pvc-alive`:
			return true, nil
		case `\\server-a\share\pvc-probe-error`:
			return false, errors.New("probe failed")
		default:
			return false, nil
		}
	}

	tests := []struct {
		desc       string
		remotePath string
		expected   []string
		expectErr  bool
	}{
		{
			desc:       "only inaccessible same-server mappings selected, case-insensitive, probe errors skipped",
			remotePath: `\\server-a\share\pvc-new`,
			expected:   []string{`\\server-a\share\pvc-dead`, `\\SERVER-A\share\pvc-dead-mixed-case`},
		},
		{
			desc:       "forward-slash remote path normalized",
			remotePath: `//server-b/share/pvc-x`,
			expected:   []string{`\\server-b\share\pvc-other-server-dead`},
		},
		{
			desc:       "unparseable remote path errors",
			remotePath: `\\`,
			expectErr:  true,
		},
	}
	for _, test := range tests {
		got, err := SelectStaleMappingsToServer(test.remotePath, mappings, pathValid)
		if test.expectErr {
			if err == nil {
				t.Errorf("%s: expected error, got none", test.desc)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", test.desc, err)
			continue
		}
		if !reflect.DeepEqual(got, test.expected) {
			t.Errorf("%s: got %v, expected %v", test.desc, got, test.expected)
		}
	}
}
