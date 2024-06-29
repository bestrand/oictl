package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

type Model struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		ID          string `yaml:"id"`
		Name        string `yaml:"name"`
		BaseModelID string `yaml:"base_model_id"`
		Meta        struct {
			ProfileImageURL    string `yaml:"profile_image_url"`
			Description        string `yaml:"description"`
			Capabilities       struct {
				Vision bool `yaml:"vision"`
			} `yaml:"capabilities"`
			SuggestionPrompts []string `yaml:"suggestion_prompts"`
			Knowledge         []struct {
				Tags string `yaml:"tags"`
			} `yaml:"knowledge"`
		} `yaml:"meta"`
		Params map[string]string `yaml:"params,omitempty"`
	} `yaml:"spec"`
}

type DocumentSource struct {
	Source     string   `yaml:"source"`
	Dir        []string `yaml:"dir,omitempty"`
	Extensions []string `yaml:"extensions,omitempty"`
}

type Documents struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Sources []DocumentSource `yaml:"sources"`
	} `yaml:"spec"`
}

type Document struct {
	CollectionName string `json:"collection_name"`
	Content        struct {
		Tags []struct {
			Name string `json:"name"`
		} `json:"tags"`
	} `json:"content"`
}

var (
	TOKEN    = os.Getenv("OI_TOKEN")
	BASE_URL = "http://localhost:8081"
)

func parseYamlFile(filePath string) (interface{}, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var doc Documents
	if err = yaml.Unmarshal(content, &doc); err == nil {
		if doc.Kind == "Documents" {
			return doc, nil
		}
	}

	var model Model
	if err = yaml.Unmarshal(content, &model); err == nil {
		if model.Kind == "Model" {
			return model, nil
		}
	}

	return nil, fmt.Errorf("unknown kind in file %s", filePath)
}

func cloneGitRepo(repoUrl, localPath string) error {
	cmd := exec.Command("git", "clone", repoUrl, localPath)
	return cmd.Run()
}

func fetchUrlContent(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch URL: %s", url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func traverseDirectory(dir string, extensions []string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (len(extensions) == 0 || hasExtension(path, extensions)) {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func hasExtension(filePath string, extensions []string) bool {
	for _, ext := range extensions {
		if strings.HasSuffix(filePath, ext) {
			return true
		}
	}
	return false
}

func handleGitSource(source string, dirs, extensions []string) ([]string, string, error) {
	workingDir, _ := os.Getwd()
	tempDir := filepath.Join(workingDir, fmt.Sprintf("temp_git_%s", uuid.New().String()))
	err := cloneGitRepo(source, tempDir)
	if err != nil {
		return nil, "", err
	}

	var sources []string
	for _, dir := range dirs {
		fullPath := filepath.Join(tempDir, dir)
		stat, err := os.Stat(fullPath)
		if err != nil {
			return nil, "", err
		}
		if stat.IsDir() {
			files, err := traverseDirectory(fullPath, extensions)
			if err != nil {
				return nil, "", err
			}
			sources = append(sources, files...)
		} else if stat.Mode().IsRegular() && hasExtension(fullPath, extensions) {
			sources = append(sources, fullPath)
		}
	}

	return sources, tempDir, nil
}

func uploadDocument(file, baseUrl, tag, originalFilename string) error {
	ragDocUrl := fmt.Sprintf("%s/rag/api/v1/doc", baseUrl)
	documentsUrl := fmt.Sprintf("%s/api/v1/documents/create", baseUrl)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", originalFilename)
	if err != nil {
		return err
	}
	fileContent, err := os.Open(file)
	if err != nil {
		return err
	}
	defer fileContent.Close()

	_, err = io.Copy(part, fileContent)
	if err != nil {
		return err
	}
	writer.Close()

	req, err := http.NewRequest("POST", ragDocUrl, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", TOKEN))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload file %s: %s - %s", file, resp.Status, string(respBody))
	}

	var responseBody map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&responseBody)
	if err != nil {
		return err
	}

	collectionName := responseBody["collection_name"].(string)
	filename := responseBody["filename"].(string)

	content := map[string]interface{}{
		"tags": []map[string]string{{"name": tag}},
	}
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return err
	}

	documentPayload := map[string]interface{}{
		"collection_name": collectionName,
		"filename":        filename,
		"name":            filename,
		"title":           filename,
		"content":         string(contentJSON),
	}

	documentBody, err := json.Marshal(documentPayload)
	if err != nil {
		return err
	}

	docReq, err := http.NewRequest("POST", documentsUrl, bytes.NewReader(documentBody))
	if err != nil {
		return err
	}
	docReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", TOKEN))
	docReq.Header.Set("Accept", "application/json")
	docReq.Header.Set("Content-Type", "application/json")

	docResp, err := client.Do(docReq)
	if err != nil {
		return err
	}
	defer docResp.Body.Close()

	if docResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(docResp.Body)
		fmt.Printf("An error occurred: failed to create document entry for file %s: %s - %s\n", file, docResp.Status, string(respBody))
		return nil
	}

	return nil
}

func getDocs(token string) ([]Document, error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/documents/", BASE_URL), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("failed to fetch documents: %s - %s", res.Status, string(bodyBytes))
	}

	var documents []Document
	if err := json.NewDecoder(res.Body).Decode(&documents); err != nil {
		return nil, err
	}

	return documents, nil
}

func fetchCollectionNamesForTags(tags []string, token string) (map[string][]string, error) {
	documents, err := getDocs(token)
	if err != nil {
		return nil, err
	}

	collections := make(map[string][]string)
	for _, tag := range tags {
		for _, doc := range documents {
			for _, docTag := range doc.Content.Tags {
				if docTag.Name == tag {
					collections[tag] = append(collections[tag], doc.CollectionName)
				}
			}
		}
	}

	return collections, nil
}

func processModel(config Model) error {
	if TOKEN == "" {
		return fmt.Errorf("OI_TOKEN environment variable is not set")
	}

	baseUrl := fmt.Sprintf("%s/api/v1/models/add", BASE_URL)

	if config.Spec.Params == nil {
		config.Spec.Params = make(map[string]string)
	}

	var tags []string
	for _, knowledge := range config.Spec.Meta.Knowledge {
		tags = append(tags, knowledge.Tags)
	}

	collections, err := fetchCollectionNamesForTags(tags, TOKEN)
	if err != nil {
		return err
	}

	var knowledgeEntries []map[string]interface{}
	for _, knowledge := range config.Spec.Meta.Knowledge {
		if len(collections[knowledge.Tags]) > 0 {
			knowledgeEntry := map[string]interface{}{
				"name":             knowledge.Tags,
				"type":             "collection",
				"collection_names": collections[knowledge.Tags],
			}
			knowledgeEntries = append(knowledgeEntries, knowledgeEntry)
		}
	}

	modelPayload := map[string]interface{}{
		"id":            config.Metadata.Name,
		"name":          config.Metadata.Name,
		"base_model_id": config.Spec.BaseModelID,
		"meta": map[string]interface{}{
			"profile_image_url": config.Spec.Meta.ProfileImageURL,
			"description":       config.Spec.Meta.Description,
			"capabilities": map[string]interface{}{
				"vision": config.Spec.Meta.Capabilities.Vision,
			},
			"suggestion_prompts": config.Spec.Meta.SuggestionPrompts,
			"knowledge":         knowledgeEntries,
		},
		"params": config.Spec.Params,
	}

	body, err := json.Marshal(modelPayload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", baseUrl, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", TOKEN))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Error processing model %s: %s - %s", config.Metadata.Name, resp.Status, string(respBody))
	}

	return nil
}

func processDirectory(directory string) ([]string, error) {
	files, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".yaml") || strings.HasSuffix(file.Name(), ".yml") {
			paths = append(paths, filepath.Join(directory, file.Name()))
		}
	}

	return paths, nil
}

func handleOictl(paths []string) error {
	documentCount := 0
	modelCount := 0

	for _, filePath := range paths {
		config, err := parseYamlFile(filePath)
		if err != nil {
			fmt.Printf("Skipped due to unknown kind in file %s\n", filePath)
			continue
		}

		switch c := config.(type) {
		case Documents:
			tag := c.Metadata.Name
			for _, source := range c.Spec.Sources {
				if strings.HasPrefix(source.Source, "git@") || strings.HasSuffix(source.Source, ".git") {
					sources, tempDir, err := handleGitSource(source.Source, source.Dir, source.Extensions)
					if err != nil {
						return err
					}
					for _, file := range sources {
						err := uploadDocument(file, BASE_URL, tag, filepath.Base(file))
						if err != nil {
							fmt.Printf("Error uploading document %s: %v\n", file, err)
							continue
						}
						documentCount++
						fmt.Printf("\rDocuments loaded: %d", documentCount)
					}
					err = os.RemoveAll(tempDir)
					if err != nil {
						fmt.Printf("\nFailed to remove temporary directory: %s\n", tempDir)
					} else {
						fmt.Printf("\nTemporary directory removed: %s\n", tempDir)
					}
				} else if strings.HasPrefix(source.Source, "http://") || strings.HasPrefix(source.Source, "https://") {
					content, err := fetchUrlContent(source.Source)
					if err != nil {
						return err
					}
					tempFile := filepath.Join(os.TempDir(), fmt.Sprintf("temp_url_%s.yaml", uuid.New().String()))
					err = os.WriteFile(tempFile, []byte(content), 0644)
					if err != nil {
						return err
					}
					err = uploadDocument(tempFile, BASE_URL, tag, source.Source)
					if err != nil {
						fmt.Printf("Error uploading document %s: %v\n", tempFile, err)
						continue
					}
					documentCount++
					fmt.Printf("\rDocuments loaded: %d", documentCount)
				} else {
					resolvedPath, _ := filepath.Abs(filepath.Join(filepath.Dir(filePath), source.Source))
					if _, err := os.Stat(resolvedPath); err == nil {
						if stat, err := os.Stat(resolvedPath); err == nil && stat.IsDir() {
							files, err := traverseDirectory(resolvedPath, source.Extensions)
							if err != nil {
								return err
							}
							for _, file := range files {
								err := uploadDocument(file, BASE_URL, tag, filepath.Base(file))
								if err != nil {
									fmt.Printf("Error uploading document %s: %v\n", file, err)
									continue
								}
								documentCount++
								fmt.Printf("\rDocuments loaded: %d", documentCount)
							}
						} else if stat.Mode().IsRegular() {
							err := uploadDocument(resolvedPath, BASE_URL, tag, filepath.Base(resolvedPath))
							if err != nil {
								fmt.Printf("Error uploading document %s: %v\n", resolvedPath, err)
								continue
							}
							documentCount++
							fmt.Printf("\rDocuments loaded: %d", documentCount)
						}
					}
				}
			}
		case Model:
			err := processModel(c)
			if err != nil {
				fmt.Printf("Error processing model %s: %v\n", filePath, err)
				continue
			}
			modelCount++
		default:
			fmt.Printf("Skipped due to unknown kind in file %s\n", filePath)
		}
	}

	if documentCount > 0 {
		fmt.Printf("\nAll Documents loaded successfully.\n")
	}
	if modelCount > 0 {
		fmt.Printf("\nAll Models loaded successfully.\n")
	}
	return nil
}

func main() {
	if len(os.Args) < 2 {
		os.Exit(1)
	}

	filePath := os.Args[1]
	resolvedPath, _ := filepath.Abs(filePath)

	var paths []string

	if stat, err := os.Stat(resolvedPath); err == nil && stat.IsDir() {
		files, err := processDirectory(resolvedPath)
		if err != nil {
			fmt.Printf("Error processing directory: %v\n", err)
			os.Exit(1)
		}
		paths = files
	} else {
		paths = append(paths, resolvedPath)
	}

	err := handleOictl(paths)
	if err != nil {
		fmt.Printf("An error occurred: %v\n", err)
	}
}
