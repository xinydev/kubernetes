/*
Copyright 2021 The Kubernetes Authors.

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

// verify that all the imports have our preferred order.
// https://github.com/kubernetes/kubeadm/issues/2515

package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/google/go-cmp/cmp"
)

var (
	includePath = flag.String("include-path", "", "only files with paths matching this regex are touched")
	ignoreFile  = flag.String("ignore-file", "zz_generated", "files matching this regex are ignored")
)

type analyzer struct {
	fset       *token.FileSet // positions are relative to fset
	failed     bool
	ignoreFile *regexp.Regexp
}

func newAnalyzer() *analyzer {
	ignoreFileRegexp, err := regexp.Compile(*ignoreFile)
	if err != nil {
		log.Fatalf("Error compiling ignore regex: %v", err)
	}

	a := &analyzer{
		fset:       token.NewFileSet(),
		ignoreFile: ignoreFileRegexp,
	}

	return a
}

// collect extracts test metadata from a file.
func (a *analyzer) collect(dir string) {
	// create the AST by parsing src.
	fs, err := parser.ParseDir(a.fset, dir, nil, parser.AllErrors|parser.ParseComments)

	if err != nil {
		fmt.Println(err)
		a.failed = true
		return
	}

	for _, p := range fs {
		files := a.filterFiles(p.Files)
		for _, file := range files {
			pathToFile := a.fset.File(file.Pos()).Name()

			if len(file.Imports) <= 1 {
				continue
			}
			var originalImports, stdlibImports, localImports, k8sImports, externalImports []string

			for i, imp := range file.Imports {
				importPath := strings.Replace(imp.Path.Value, "\"", "", -1)
				parts := strings.Split(importPath, "/")

				// if the original imports have blank line, need to add a blank line for originalImports too
				if i != 0 && a.lineAt(imp.Pos()) > 1+a.lineAt(file.Imports[i-1].End()) {
					originalImports = append(originalImports, "")
				}
				originalImports = append(originalImports, importPath)

				if !strings.Contains(parts[0], ".") {
					// standard library
					stdlibImports = append(stdlibImports, importPath)
				} else if strings.HasPrefix(importPath, "k8s.io/kubernetes") {
					// local imports
					localImports = append(localImports, importPath)
				} else if strings.Contains(parts[0], "k8s.io") {
					// other *.k8s.io imports
					k8sImports = append(k8sImports, importPath)
				} else {
					// external repositories
					externalImports = append(externalImports, importPath)
				}
			}

			orderImports := []string{}
			for _, imps := range [][]string{
				stdlibImports,
				externalImports,
				k8sImports,
				localImports,
			} {
				sort.Strings(imps)
				orderImports = append(orderImports, imps...)
				if len(imps) > 0 {
					orderImports = append(orderImports, "")
				}
			}
			// remove the last empty line, if any
			if orderImports[len(orderImports)-1] == "" {
				orderImports = orderImports[:len(orderImports)-1]
			}

			if diff := cmp.Diff(strings.Join(originalImports, "\n"), strings.Join(orderImports, "\n")); diff != "" {
				a.failed = true
				fmt.Printf("%s (-got +want):\n%s", pathToFile, diff)
			}
		}
	}
}

func (a *analyzer) lineAt(pos token.Pos) int {
	return a.fset.PositionFor(pos, false).Line
}

func (a *analyzer) filterFiles(fs map[string]*ast.File) []*ast.File {
	var files []*ast.File
	for fileName, f := range fs {
		if a.ignoreFile.MatchString(fileName) {
			continue
		}
		files = append(files, f)
	}
	return files
}

type collector struct {
	dirs        []string
	includePath *regexp.Regexp
}

// handlePath walks the filesystem recursively, collecting directories,
// ignoring some unneeded directories (hidden,vendor).
func (c *collector) handlePath(path string, info os.FileInfo, err error) error {
	if err != nil {
		return err
	}
	if info.IsDir() {
		// Ignore hidden directories (.git, .cache, etc)
		if len(path) > 1 && path[0] == '.' ||
			// OS-specific vendor code tends to be imported by OS-specific
			// packages. We recursively typecheck imported vendored packages for
			// each OS, but don't typecheck everything for every OS.
			path == "vendor" ||
			path == "_output" {
			return filepath.SkipDir
		}
		if c.includePath.MatchString(path) {
			c.dirs = append(c.dirs, path)
		}
	}
	return nil
}

func main() {
	flag.Parse()
	args := flag.Args()

	if len(args) == 0 {
		args = append(args, ".")
	}

	includePathRegexp, err := regexp.Compile(*includePath)
	if err != nil {
		log.Fatalf("Error compiling import path regex: %v", err)
	}
	c := collector{includePath: includePathRegexp}
	for _, arg := range args {
		err := filepath.Walk(arg, c.handlePath)
		if err != nil {
			log.Fatalf("Error walking: %v", err)
		}
	}
	sort.Strings(c.dirs)

	fmt.Println("checking-imports-order: ")
	a := newAnalyzer()
	for _, dir := range c.dirs {
		a.collect(dir)
	}
	if a.failed {
		os.Exit(1)
	}
}
