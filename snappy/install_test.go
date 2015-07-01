// -*- Mode: Go; indent-tabs-mode: t -*-

/*
 * Copyright (C) 2014-2015 Canonical Ltd
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License version 3 as
 * published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package snappy

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"

	. "gopkg.in/check.v1"
	"launchpad.net/snappy/partition"
	"launchpad.net/snappy/progress"
)

func makeCloudInitMetaData(c *C, content string) string {
	w, err := ioutil.TempFile("", "meta-data")
	c.Assert(err, IsNil)
	w.Write([]byte(content))
	w.Sync()
	return w.Name()
}

func (s *SnapTestSuite) TestInstallInstall(c *C) {
	snapFile := makeTestSnapPackage(c, "")
	name, err := Install(snapFile, AllowUnauthenticated|DoInstallGC, &progress.NullProgress{})
	c.Assert(err, IsNil)
	c.Check(name, Equals, "foo")
}

func (s *SnapTestSuite) installThree(c *C, flags InstallFlags) {
	snapDataHomeGlob = filepath.Join(s.tempdir, "home", "*", "apps")
	homeDir := filepath.Join(s.tempdir, "home", "user1", "apps")
	homeData := filepath.Join(homeDir, "foo", "1.0")
	err := os.MkdirAll(homeData, 0755)
	c.Assert(err, IsNil)

	packageYaml := `name: foo
icon: foo.svg
vendor: Foo Bar <foo@example.com>
`
	snapFile := makeTestSnapPackage(c, packageYaml+"version: 1.0")
	_, err = Install(snapFile, flags, &progress.NullProgress{})
	c.Assert(err, IsNil)

	snapFile = makeTestSnapPackage(c, packageYaml+"version: 2.0")
	_, err = Install(snapFile, flags, &progress.NullProgress{})
	c.Assert(err, IsNil)

	snapFile = makeTestSnapPackage(c, packageYaml+"version: 3.0")
	_, err = Install(snapFile, flags, &progress.NullProgress{})
	c.Assert(err, IsNil)
}

// check that on install we remove all but the two newest package versions
func (s *SnapTestSuite) TestClickInstallGCSimple(c *C) {
	s.installThree(c, AllowUnauthenticated|DoInstallGC)

	globs, err := filepath.Glob(filepath.Join(snapAppsDir, "foo.sideload", "*"))
	c.Assert(err, IsNil)
	c.Assert(globs, HasLen, 2+1) // +1 for "current"
}

// check that if flags does not include DoInstallGC, no gc is done
func (s *SnapTestSuite) TestClickInstallGCSuppressed(c *C) {
	s.installThree(c, AllowUnauthenticated)

	globs, err := filepath.Glob(filepath.Join(snapAppsDir, "foo.sideload", "*"))
	c.Assert(err, IsNil)
	c.Assert(globs, HasLen, 3+1) // +1 for "current"
}

func (s *SnapTestSuite) TestInstallAppTwiceFails(c *C) {
	snapPackage := makeTestSnapPackage(c, "name: foo\nversion: 2\nvendor: foo")
	snapR, err := os.Open(snapPackage)
	c.Assert(err, IsNil)
	defer snapR.Close()

	var dlURL, iconURL string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/details/foo":
			io.WriteString(w, `{
"package_name": "foo",
"version": "2",
"origin": "test",
"anon_download_url": "`+dlURL+`",
"icon_url": "`+iconURL+`"
}`)
		case "/dl":
			snapR.Seek(0, 0)
			io.Copy(w, snapR)
		case "/icon":
			fmt.Fprintf(w, "")
		default:
			panic("unexpected url path: " + r.URL.Path)
		}
	}))
	c.Assert(mockServer, NotNil)
	defer mockServer.Close()

	dlURL = mockServer.URL + "/dl"
	iconURL = mockServer.URL + "/icon"

	storeDetailsURI, err = url.Parse(mockServer.URL + "/details/")
	c.Assert(err, IsNil)

	name, err := Install("foo", 0, &progress.NullProgress{})
	c.Assert(err, IsNil)
	c.Check(name, Equals, "foo")

	_, err = Install("foo", 0, &progress.NullProgress{})
	c.Assert(err, ErrorMatches, ".*"+ErrAlreadyInstalled.Error())
}

func (s *SnapTestSuite) TestInstallAppPackageNameFails(c *C) {
	// install one:
	yamlFile, err := makeInstalledMockSnap(s.tempdir, "")
	c.Assert(err, IsNil)
	pkgdir := filepath.Dir(filepath.Dir(yamlFile))

	c.Assert(os.MkdirAll(filepath.Join(pkgdir, ".click", "info"), 0755), IsNil)
	c.Assert(ioutil.WriteFile(filepath.Join(pkgdir, ".click", "info", "hello-app.manifest"), []byte(`{"name": "hello-app"}`), 0644), IsNil)
	ag := &progress.NullProgress{}
	part, err := NewInstalledSnapPart(yamlFile, "potato")
	c.Assert(err, IsNil)
	c.Assert(part.activate(true, ag), IsNil)
	current := ActiveSnapByName("hello-app")
	c.Assert(current, NotNil)

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/details/hello-app.potato":
			io.WriteString(w, `{
"origin": "potato",
"package_name": "hello-app",
"version": "2",
"anon_download_url": "blah"
}`)
		default:
			panic("unexpected url path: " + r.URL.Path)
		}
	}))

	storeDetailsURI, err = url.Parse(mockServer.URL + "/details/")
	c.Assert(err, IsNil)

	c.Assert(mockServer, NotNil)
	defer mockServer.Close()

	_, err = Install("hello-app.potato", 0, ag)
	c.Assert(err, ErrorMatches, ".*"+ErrPackageNameAlreadyInstalled.Error())
}

func (s *SnapTestSuite) TestUpdate(c *C) {
	snapPackagev1 := makeTestSnapPackage(c, "name: foo\nversion: 1\nvendor: foo")
	name, err := Install(snapPackagev1, AllowUnauthenticated|DoInstallGC, &progress.NullProgress{})
	c.Assert(err, IsNil)
	c.Assert(name, Equals, "foo")

	snapPackagev2 := makeTestSnapPackage(c, "name: foo\nversion: 2\nvendor: foo")

	snapR, err := os.Open(snapPackagev2)
	c.Assert(err, IsNil)
	defer snapR.Close()

	// details
	var dlURL, iconURL string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/details/foo":
			io.WriteString(w, `{
"package_name": "foo",
"version": "2",
"origin": "sideload",
"anon_download_url": "`+dlURL+`",
"icon_url": "`+iconURL+`"
}`)
		case "/dl":
			snapR.Seek(0, 0)
			io.Copy(w, snapR)
		case "/icon":
			fmt.Fprintf(w, "")
		default:
			panic("unexpected url path: " + r.URL.Path)
		}
	}))
	c.Assert(mockServer, NotNil)
	defer mockServer.Close()

	dlURL = mockServer.URL + "/dl"
	iconURL = mockServer.URL + "/icon"

	storeDetailsURI, err = url.Parse(mockServer.URL + "/details/")
	c.Assert(err, IsNil)

	// bulk
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{
	"package_name": "foo",
	"version": "2",
	"origin": "sideload",
	"anon_download_url": "`+dlURL+`",
	"icon_url": "`+iconURL+`"
}]`)
	}))

	storeBulkURI, err = url.Parse(mockServer.URL)
	c.Assert(err, IsNil)

	c.Assert(mockServer, NotNil)
	defer mockServer.Close()

	// system image
	newPartition = func() (p partition.Interface) {
		return new(MockPartition)
	}
	defer func() { newPartition = newPartitionImpl }()

	tempdir := c.MkDir()
	systemImageRoot = tempdir

	makeFakeSystemImageChannelConfig(c, filepath.Join(tempdir, systemImageChannelConfig), "1")
	// setup fake /other partition
	makeFakeSystemImageChannelConfig(c, filepath.Join(tempdir, "other", systemImageChannelConfig), "2")

	siServer := runMockSystemImageWebServer()
	defer siServer.Close()

	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, fmt.Sprintf(mockSystemImageIndexJSONTemplate, "1"))
	}))
	c.Assert(mockServer, NotNil)
	defer mockServer.Close()

	systemImageServer = mockServer.URL

	// the test
	updates, err := Update(0, &progress.NullProgress{})
	c.Assert(err, IsNil)
	c.Assert(updates, HasLen, 1)
	c.Check(updates[0].Name(), Equals, "foo")
	c.Check(updates[0].Version(), Equals, "2")
}
