package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func main() {
	host := flag.String("host", "", "host domain")
	file := flag.String("file", "", "data file")
	resourceType := flag.String("type", "", "resource type name")
	outputPath := flag.String("out", "", "output directory")
	idKeys := flag.String("id", "id", "ID column names (seperate by \",\")")
	filterKeys := flag.String("filter", "", "enable filtering on columns (seperate by \",\")")
	perPage := flag.Int("perPage", 10, "items per page")
	flag.Parse()
	if *host == "" {
		log.Fatal("host is required")
	}
	if *file == "" {
		log.Fatal("file is required")
	}
	if *outputPath == "" {
		log.Fatal("output path is required")
	}
	filename := path.Base(*file)
	if *resourceType == "" {
		inferType := filename[0 : len(filename)-len(filepath.Ext(filename))]
		resourceType = &inferType
	}
	idKeysSlice := strings.Split(*idKeys, ",")
	filterKeysSlice := strings.Split(*filterKeys, ",")

	f, err := os.Open(*file)
	if err != nil {
		log.Fatal(err)
	}

	r := csv.NewReader(f)

	// read header
	header, err := r.Read()
	if err != nil {
		log.Fatal(err)
	}

	// read rows
	rows, err := r.ReadAll()
	if err != nil {
		log.Fatal(err)
	}

	if err = os.RemoveAll(*outputPath); err != nil {
		log.Fatal(err)
	}
	if err = os.MkdirAll(*outputPath, os.ModePerm); err != nil {
		log.Fatal(err)
	}

	docs := buildPaginatedIndexDocuments(*host, *resourceType, header, rows, idKeysSlice, *perPage)
	for i, doc := range docs {
		bytes, err := json.Marshal(doc)
		if err != nil {
			log.Fatal(err)
		}
		out := filepath.Join(*outputPath, fmt.Sprintf("%s-%d.json", *resourceType, i))
		err = ioutil.WriteFile(out, bytes, 0666)
		if err != nil {
			log.Fatal(err)
		}
	}

	objDocs := buildObjectDocuments(*resourceType, header, rows, idKeysSlice)
	for _, doc := range objDocs {
		bytes, err := json.Marshal(doc)
		if err != nil {
			log.Fatal(err)
		}
		outDir := filepath.Join(*outputPath, *resourceType)
		os.MkdirAll(outDir, os.ModePerm)
		out := filepath.Join(outDir, fmt.Sprintf("%s.json", doc.Data[0].ID))
		err = ioutil.WriteFile(out, bytes, 0666)
		if err != nil {
			log.Fatal(err)
		}
	}
	// build _redirects
	rewrites := ""
	for _, filterKey := range filterKeysSlice {
		filteredDocs := buildFilteredDocumentsForColumn(*host, *resourceType, header, rows, idKeysSlice, *perPage, filterKey)

		for idx, docs := range filteredDocs {
			for i, doc := range docs {
				outDir := filepath.Join(*outputPath, filterKey+"-filtered", idx)
				os.MkdirAll(outDir, os.ModePerm)
				out := filepath.Join(outDir, fmt.Sprintf("%d.json", i))
				file, err := os.Create(out)
				if err != nil {
					log.Fatal(err)
				}
				enc := json.NewEncoder(file)
				enc.SetEscapeHTML(false)
				err = enc.Encode(doc)
				if err != nil {
					log.Fatal(err)
				}
			}
		}
		rewrites += fmt.Sprintf("/%s.json filter[%s]=:filter page=:p /%s-filtered/:filter/:p.json 200!\n", *resourceType, filterKey, filterKey)
		rewrites += fmt.Sprintf("/%s.json filter[%s]=:filter /%s-filtered/:filter/0.json 200!\n", *resourceType, filterKey, filterKey)
	}
	rewrites += fmt.Sprintf("/%s.json page=:p /%s-:p.json 200!\n", *resourceType, *resourceType)
	rewrites += fmt.Sprintf("/%s.json /%s-0.json 200!\n", *resourceType, *resourceType)

	ioutil.WriteFile(filepath.Join(*outputPath, "_redirects"), []byte(rewrites), 0666)

	// build _headers
	headers := fmt.Sprintf("/*\n")
	headers += fmt.Sprintf("  Access-Control-Allow-Origin: *\n")
	headers += fmt.Sprintf("  content-type: application/json; charset=utf-8\n")
	ioutil.WriteFile(filepath.Join(*outputPath, "_headers"), []byte(headers), 0666)
}

type Document struct {
	Meta  map[string]interface{} `json:"meta,omitempty"`
	Links map[string]string      `json:"links,omitempty"`
	Data  []Object               `json:"data"`
}

type Object struct {
	Type       string                 `json:"type"`
	ID         string                 `json:"id"`
	Attributes map[string]interface{} `json:"attributes"`
}

func buildFilteredDocumentsForColumn(host string, objType string, header []string, rows [][]string, idKeys []string, pageSize int, filterKey string) map[string][]Document {
	indexedObjects := make(map[string][]Object)
	indexedDocs := make(map[string][]Document)

	for _, row := range rows {
		obj := buildObject(objType, header, row, idKeys)
		idx := obj.Attributes[filterKey].(string)
		if _, exists := indexedObjects[idx]; !exists {
			indexedObjects[idx] = make([]Object, 0)
		}
		indexedObjects[idx] = append(indexedObjects[idx], obj)

		if len(indexedObjects[idx]) == pageSize {
			doc := Document{}
			doc.Data = indexedObjects[idx]
			if _, exists := indexedDocs[idx]; !exists {
				indexedDocs[idx] = make([]Document, 0)
			}
			indexedDocs[idx] = append(indexedDocs[idx], doc)
			indexedObjects[idx] = make([]Object, 0)
		}
	}
	for idx, objs := range indexedObjects {
		if len(objs) > 0 {
			doc := Document{}
			doc.Data = objs
			if _, exists := indexedDocs[idx]; !exists {
				indexedDocs[idx] = make([]Document, 0)
			}
			indexedDocs[idx] = append(indexedDocs[idx], doc)
		}
	}
	// set meta & links
	for idx, docs := range indexedDocs {
		for i, doc := range docs {
			doc.Meta = map[string]interface{}{
				"total-pages": len(docs),
			}

			links := make(map[string]string)
			if i > 0 {
				links["prev"] = fmt.Sprintf("%s/%s.json?filter[%s]=%s&page=%d", host, objType, filterKey, idx, i-1)
			}
			if i < len(docs)-1 {
				links["next"] = fmt.Sprintf("%s/%s.json?filter[%s]=%s&page=%d", host, objType, filterKey, idx, i+1)
			}

			links["first"] = fmt.Sprintf("%s/%s.json?filter[%s]=%s&page=0", host, objType, filterKey, idx)
			links["last"] = fmt.Sprintf("%s/%s.json?filter[%s]=%s&page=%d", host, objType, filterKey, idx, len(docs)-1)
			docs[i].Links = links
		}
	}
	return indexedDocs
}

func buildPaginatedIndexDocuments(host string, objType string, header []string, rows [][]string, idKeys []string, pageSize int) []Document {
	var docs []Document

	var objs []Object
	for _, row := range rows {
		obj := buildObject(objType, header, row, idKeys)

		objs = append(objs, obj)

		if (len(objs)) == pageSize {
			doc := Document{}
			doc.Data = objs
			docs = append(docs, doc)
			objs = make([]Object, 0)
		}
	}

	if len(objs) > 0 {
		doc := Document{}
		doc.Data = objs
		docs = append(docs, doc)
		objs = make([]Object, 0)
	}
	// set meta
	for i := range docs {
		docs[i].Meta = map[string]interface{}{
			"total-pages": len(docs),
		}
	}
	// set links
	for i := range docs {
		links := make(map[string]string)
		if i > 0 {
			links["prev"] = fmt.Sprintf("%s/%s.json?page=%d", host, objType, i-1)
		}
		if i < len(docs)-1 {
			links["next"] = fmt.Sprintf("%s/%s.json?page=%d", host, objType, i+1)
		}

		links["first"] = fmt.Sprintf("%s/%s.json?page=0", host, objType)
		links["last"] = fmt.Sprintf("%s/%s.json?page=%d", host, objType, len(docs)-1)
		docs[i].Links = links
	}

	return docs
}

func buildObjectDocuments(objType string, header []string, rows [][]string, idKeys []string) []Document {
	var docs []Document

	for _, row := range rows {
		obj := buildObject(objType, header, row, idKeys)
		doc := Document{Data: []Object{obj}}
		docs = append(docs, doc)
	}

	return docs
}

func buildObject(objType string, header []string, row []string, idKeys []string) Object {
	obj := Object{Type: objType}
	kv := row2map(header, row)
	id := make([]string, 0)
	for _, key := range idKeys {
		id = append(id, kv[key].(string))
	}
	obj.ID = strings.Join(id, "-")
	obj.Attributes = kv

	return obj
}

// row2map normalize each columns name and build a map for the row
func row2map(header []string, row []string) map[string]interface{} {
	r := make(map[string]interface{})
	for i, h := range header {
		if looksLikeJSONObject(row[i]) || looksLikeJSONArray(row[i]) {
			var m interface{}
			err := json.Unmarshal([]byte(row[i]), &m)
			if err != nil {
				log.Fatal(err)
			}
			r[strings.ToLower(h)] = m
		} else {
			r[strings.ToLower(h)] = row[i]
		}
	}
	return r
}

func looksLikeJSONObject(str string) bool {
	return strings.HasPrefix(str, "{") && strings.HasSuffix(str, "}")
}

func looksLikeJSONArray(str string) bool {
	return strings.HasPrefix(str, "[") && strings.HasSuffix(str, "]")
}
