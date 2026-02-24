package utils

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileInfo represents information about an uploaded file
type FileInfo struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64 encoded
}

// DecodeBase64 decodes a base64 string to bytes
func DecodeBase64(data string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(data)
}

// ReadFileAsBase64 reads a file and returns its base64 encoded content
func ReadFileAsBase64(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// GetMimeType returns the MIME type based on file extension
func GetMimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))

	mimeTypes := map[string]string{
		// Images
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
		".gif":  "image/gif",
		".webp": "image/webp",

		// Documents
		".pdf":  "application/pdf",
		".txt":  "text/plain",
		".md":   "text/markdown",
		".json": "application/json",
		".xml":  "application/xml",
		".csv":  "text/csv",

		// Code
		".go":   "text/x-go",
		".js":   "text/javascript",
		".ts":   "text/typescript",
		".py":   "text/x-python",
		".java": "text/x-java",
		".c":    "text/x-c",
		".cpp":  "text/x-c++",
		".html": "text/html",
		".css":  "text/css",
	}

	if mimeType, ok := mimeTypes[ext]; ok {
		return mimeType
	}

	return "application/octet-stream"
}

// ValidateFile validates file size and type
func ValidateFile(path string, maxSizeMB int) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("file not found: %w", err)
	}

	// Check file size
	maxSize := int64(maxSizeMB * 1024 * 1024)
	if info.Size() > maxSize {
		return fmt.Errorf("file too large: %d MB (max %d MB)", info.Size()/(1024*1024), maxSizeMB)
	}

	return nil
}

// IsImageFile checks if the file is an image based on extension
func IsImageFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	imageExts := map[string]bool{
		".jpg":  true,
		".jpeg": true,
		".png":  true,
		".gif":  true,
		".webp": true,
	}
	return imageExts[ext]
}

// IsCodeFile checks if the file is a code/text file
func IsCodeFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	codeExts := map[string]bool{
		".go": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true,
		".py": true, ".java": true, ".c": true, ".cpp": true, ".h": true,
		".rs": true, ".rb": true, ".php": true, ".swift": true, ".kt": true,
		".html": true, ".css": true, ".scss": true, ".json": true, ".xml": true,
		".yaml": true, ".yml": true, ".toml": true, ".md": true, ".txt": true,
		".sh": true, ".bash": true, ".sql": true, ".svelte": true, ".vue": true,
	}
	return codeExts[ext]
}

// GetLanguageFromExt returns language identifier for syntax highlighting
func GetLanguageFromExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	langMap := map[string]string{
		".go": "go", ".js": "javascript", ".ts": "typescript",
		".tsx": "typescript", ".jsx": "javascript",
		".py": "python", ".java": "java", ".c": "c", ".cpp": "cpp", ".h": "c",
		".rs": "rust", ".rb": "ruby", ".php": "php", ".swift": "swift", ".kt": "kotlin",
		".html": "html", ".css": "css", ".scss": "scss", ".json": "json", ".xml": "xml",
		".yaml": "yaml", ".yml": "yaml", ".toml": "toml", ".md": "markdown",
		".sh": "bash", ".bash": "bash", ".sql": "sql",
		".svelte": "svelte", ".vue": "vue",
	}
	if lang, ok := langMap[ext]; ok {
		return lang
	}
	return ""
}

// ReadFileLines reads specific lines from a file (1-indexed, inclusive).
// Returns the extracted content, total line count, and any error.
func ReadFileLines(path string, startLine, endLine int) (string, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB line buffer
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum >= startLine && lineNum <= endLine {
			lines = append(lines, scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil {
		return "", 0, fmt.Errorf("failed to read file: %w", err)
	}

	totalLines := lineNum

	if startLine < 1 {
		startLine = 1
	}
	if endLine > totalLines {
		endLine = totalLines
	}
	if startLine > totalLines {
		return "", totalLines, fmt.Errorf("start line %d exceeds total lines %d", startLine, totalLines)
	}

	return strings.Join(lines, "\n"), totalLines, nil
}

// ProcessFile processes a file and returns FileInfo
func ProcessFile(path string, maxSizeMB int) (*FileInfo, error) {
	// Validate file
	if err := ValidateFile(path, maxSizeMB); err != nil {
		return nil, err
	}

	// Get file info
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	// Read file as base64
	data, err := ReadFileAsBase64(path)
	if err != nil {
		return nil, err
	}

	// Get MIME type
	mimeType := GetMimeType(path)

	return &FileInfo{
		Name:     filepath.Base(path),
		Path:     path,
		Size:     info.Size(),
		MimeType: mimeType,
		Data:     data,
	}, nil
}
