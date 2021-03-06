// Copyright 2013 Dmitry Chestnykh. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package layouts implements text templating and handling of layouts.
package layouts

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"text/template"

	"github.com/dchest/kkr/metafile"
)

type FuncMap template.FuncMap

type SiteContext interface {
	LayoutData() interface{}
	LayoutFuncs() FuncMap
}

type PageContext interface {
	Meta() map[string]interface{}
	Content() string
	URL() string
	FileInfo() os.FileInfo
}

// Layout represends a layout.
type Layout struct {
	Name       string
	ParentName string
	Template   *template.Template
}

type Collection struct {
	layouts map[string]*Layout
	context SiteContext
}

func NewCollection(context SiteContext) *Collection {
	return &Collection{
		layouts: make(map[string]*Layout),
		context: context,
	}
}

func (c *Collection) newLayout(name string, parentName string, content string) (l *Layout, err error) {
	t, err := template.New(name).Funcs(template.FuncMap(c.context.LayoutFuncs())).Parse(content)
	if err != nil {
		return nil, err
	}
	return &Layout{
		Name:       name,
		ParentName: parentName,
		Template:   t,
	}, nil
}

func layoutNameFromMeta(meta map[string]interface{}) (string, error) {
	l, ok := meta["layout"]
	if ok {
		name, ok := l.(string)
		if !ok {
			return "", fmt.Errorf("`layout` must be a string")
		}
		return name, nil
	}
	return "", nil
}

func (c *Collection) newLayoutFromFile(filename string, stripExtension bool) (l *Layout, err error) {
	f, err := metafile.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	name := filepath.Base(filename)
	if stripExtension {
		name = name[:len(name)-len(filepath.Ext(name))]
	}
	parentName, err := layoutNameFromMeta(f.Meta())
	if err != nil {
		return nil, err
	}
	content, err := f.Content()
	if err != nil {
		return nil, err
	}
	return c.newLayout(name, parentName, string(content))
}

func (c *Collection) AddFile(filename string) error {
	l, err := c.newLayoutFromFile(filename, true)
	if err != nil {
		return err
	}
	c.layouts[l.Name] = l
	log.Printf("L %s", l.Name)
	return nil
}

func (c *Collection) AddDir(dirname string) error {
	return filepath.Walk(dirname, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		return c.AddFile(path)
	})
}

func (c *Collection) renderLayout(l *Layout, pageContext PageContext, content string) (out string, err error) {
	// Execute current layout.
	var buf bytes.Buffer
	err = l.Template.Execute(&buf, struct {
		Site    interface{}
		Page    interface{}
		Content string
	}{
		c.context.LayoutData(),
		pageContext.Meta(),
		content,
	})
	if err != nil {
		return
	}

	out = buf.String()

	if l.ParentName != "" && l.ParentName != "none" {
		// Execute parent layout on output.
		parentLayout, ok := c.layouts[l.ParentName]
		if !ok {
			return "", fmt.Errorf("layout %q not found", l.ParentName)
		}
		return c.renderLayout(parentLayout, pageContext, out)
	}
	return out, nil
}

func (c *Collection) RenderPage(pageContext PageContext, defaultLayoutName string) (out string, err error) {
	if renderedCache != nil {
		// Check cache
		if rendered, ok := renderedCache.Get(pageContext.URL(), pageContext.FileInfo()); ok {
			return rendered, nil
		}
	}
	layoutName, err := layoutNameFromMeta(pageContext.Meta())
	if err != nil {
		return
	}
	if layoutName == "" {
		layoutName = defaultLayoutName
	}
	p, err := c.newLayout("", layoutName, pageContext.Content())
	if err != nil {
		return
	}
	out, err = c.renderLayout(p, pageContext, pageContext.Content())
	if err == nil && renderedCache != nil {
		// Add to cache
		renderedCache.Put(pageContext.URL(), pageContext.FileInfo(), out)
	}
	return out, err
}

type cache struct {
	mu sync.Mutex
	m  map[string]cacheEntry
}

type cacheEntry struct {
	fi       os.FileInfo
	rendered string
}

func (c *cache) Get(name string, fi os.FileInfo) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[name]
	if !ok {
		return "", false
	}
	if e.fi.ModTime() != fi.ModTime() || e.fi.Size() != fi.Size() || e.fi.Mode() != fi.Mode() {
		// This entry changed, delete it from cache.
		delete(c.m, name)
		return "", false
	}
	return e.rendered, true
}

func (c *cache) Put(name string, fi os.FileInfo, rendered string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[name] = cacheEntry{
		fi:       fi,
		rendered: rendered,
	}
}

var renderedCache *cache

func EnableCache(value bool) {
	if value {
		renderedCache = &cache{
			m: make(map[string]cacheEntry),
		}
	} else {
		renderedCache = nil
	}
}
