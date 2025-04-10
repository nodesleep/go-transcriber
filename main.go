package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TranscriptionResponse represents the response from Groq API
type TranscriptionResponse struct {
	Text string `json:"text"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error string `json:"error"`
}

// SuccessResponse represents a successful transcription response
type SuccessResponse struct {
	Transcription string `json:"transcription"`
}

func main() {
	r := gin.Default()

	// Configure CORS
	config := cors.DefaultConfig()

	// I tested this from my Vite/Vue app
	config.AllowOrigins = []string{"http://localhost:5173"}
	config.AllowMethods = []string{"GET", "POST", "OPTIONS"}
	config.AllowHeaders = []string{"Origin", "Content-Type", "Authorization"}
	r.Use(cors.New(config))

	// Set up routes
	r.POST("/api/transcribe", transcribeAudio)

	// Start server
	r.Run(":8080")
}

func transcribeAudio(c *gin.Context) {
	// Get file from request
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "No file provided"})
		return
	}
	defer file.Close()

	tempFiles := []string{}
	
	// Create temp directory if it doesn't exist
	tempDir := os.TempDir()
	
	// Save uploaded file to temp location
	tempRawAudioFile := filepath.Join(tempDir, uuid.New().String()+"-"+header.Filename)
	tempFile, err := os.Create(tempRawAudioFile)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to create temp file"})
		return
	}
	
	_, err = io.Copy(tempFile, file)
	tempFile.Close()
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to save uploaded file"})
		return
	}
	
	tempFiles = append(tempFiles, tempRawAudioFile)
	
	// Preprocess audio file
	tempPreProcessedAudioFile := filepath.Join(tempDir, uuid.New().String()+"-preprocessed.flac")
	err = preprocessAudioFile(tempRawAudioFile, tempPreProcessedAudioFile)
	if err != nil {
		deleteFiles(tempFiles)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to preprocess audio: " + err.Error()})
		return
	}
	tempFiles = append(tempFiles, tempPreProcessedAudioFile)
	
	// Get audio chunk data
	chunkData, err := getAudioChunkData(tempPreProcessedAudioFile)
	if err != nil {
		deleteFiles(tempFiles)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to analyze audio: " + err.Error()})
		return
	}
	
	// Chunkify audio file
	chunks, err := chunkifyAudioFile(tempPreProcessedAudioFile, chunkData)
	if err != nil {
		deleteFiles(tempFiles)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to chunk audio: " + err.Error()})
		return
	}
	tempFiles = append(tempFiles, chunks...)
	
	// Transcribe chunks in parallel
	apiKey := "" // Get your own, friend. :)
	apiURL := "https://api.groq.com/openai/v1/audio/transcriptions"
	
	// Use a WaitGroup to track when all goroutines are done
	var wg sync.WaitGroup

	// Use a buffered channel as a semaphore to limit concurrency
	// Process 5 chunks at a time
	semaphore := make(chan struct{}, 5)
	
	// Create a mutex to protect concurrent writes to the results slice
	var mutex sync.Mutex
	transcriptionResults := make([]string, len(chunks))
	
	for i, chunk := range chunks {
		wg.Add(1)
		go func(i int, chunkPath string) {
			defer wg.Done()
			
			// Acquire a token from the semaphore
			semaphore <- struct{}{}

			// Release the token when done
			defer func() { <-semaphore }()
			
			transcriptionText, err := transcribeChunk(chunkPath, apiURL, apiKey)
			
			mutex.Lock()
			if err != nil {
				log.Printf("Error transcribing chunk %d: %v", i, err)
				transcriptionResults[i] = ""
			} else {
				transcriptionResults[i] = transcriptionText
			}
			mutex.Unlock()
		}(i, chunk)
	}
	
	// Wait for all transcription tasks to complete
	wg.Wait()
	
	// Filter out empty (failed) transcriptions and combine
	var validTranscriptions []string
	for _, text := range transcriptionResults {
		if text != "" {
			validTranscriptions = append(validTranscriptions, text)
		}
	}
	
	// Clean up temp files
	deleteFiles(tempFiles)
	
	// Return the combined transcription
	c.JSON(http.StatusOK, SuccessResponse{Transcription: strings.Join(validTranscriptions, "")})
}

func preprocessAudioFile(inputFilePath, outputFilePath string) error {
	cmd := exec.Command(
		"ffmpeg",
		"-i", inputFilePath,
		"-ar", "16000",
		"-ac", "1",
		"-c:a", "flac",
		"-map", "0:a",
		outputFilePath,
	)
	
	return cmd.Run()
}

// ChunkData represents information about audio chunks
type ChunkData struct {
	DurationMs float64
	ChunkMs    float64
	OverlapMs  float64
	TotalChunks int
}

func getAudioChunkData(filePath string) (ChunkData, error) {
	// Set default chunk parameters in seconds
	chunkLength := 120.0
	overlap := 1.0
	
	// Run ffprobe to get audio duration
	cmd := exec.Command(
		"ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "json",
		filePath,
	)
	
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return ChunkData{}, err
	}
	
	// Parse the JSON output
	var result struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		return ChunkData{}, err
	}
	
	duration, err := strconv.ParseFloat(result.Format.Duration, 64)
	if err != nil {
		return ChunkData{}, fmt.Errorf("unable to parse duration: %w", err)
	}
	
	durationMs := duration * 1000
	chunkMs := chunkLength * 1000
	overlapMs := overlap * 1000
	totalChunks := int(durationMs/(chunkMs-overlapMs)) + 1
	
	return ChunkData{
		DurationMs:  durationMs,
		ChunkMs:     chunkMs,
		OverlapMs:   overlapMs,
		TotalChunks: totalChunks,
	}, nil
}

func chunkifyAudioFile(filePath string, chunkData ChunkData) ([]string, error) {
	chunkIdentifier := uuid.New().String()
	chunks := make([]string, 0, chunkData.TotalChunks)
	
	// Use a WaitGroup to track when all goroutines are done
	var wg sync.WaitGroup

	// Create a buffered channel as a semaphore for concurrency control
	numCPU := len(os.Getenv("GOMAXPROCS"))
	if numCPU <= 0 {
		numCPU = 4 // Default to 4 if GOMAXPROCS is not set
	}
	semaphore := make(chan struct{}, numCPU)
	
	// Create a mutex to protect concurrent writes to the chunks slice
	var mutex sync.Mutex
	var errors []string
	
	tempDir := os.TempDir()
	
	for i := 0; i < chunkData.TotalChunks; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			
			// Acquire a token from the semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			
			startMs := float64(i) * (chunkData.ChunkMs - chunkData.OverlapMs)
			endMs := startMs + chunkData.ChunkMs
			if endMs > chunkData.DurationMs {
				endMs = chunkData.DurationMs
			}
			
			segmentDurationSec := (endMs - startMs) / 1000
			startSec := startMs / 1000
			
			outputPath := filepath.Join(tempDir, fmt.Sprintf("%s_%d.flac", chunkIdentifier, i+1))
			
			err := createAudioChunkFile(filePath, outputPath, startSec, segmentDurationSec)
			
			mutex.Lock()
			if err != nil {
				errors = append(errors, fmt.Sprintf("Error creating chunk %d: %v", i, err))
			} else {
				chunks = append(chunks, outputPath)
			}
			mutex.Unlock()
		}(i)
	}
	
	// Wait for all chunk creation tasks to complete
	wg.Wait()
	
	if len(errors) > 0 {
		return chunks, fmt.Errorf("some chunks failed: %s", strings.Join(errors, "; "))
	}
	
	return chunks, nil
}

func createAudioChunkFile(filePath, outputPath string, startSeconds, duration float64) error {
	cmd := exec.Command(
		"ffmpeg",
		"-i", filePath,
		"-ss", fmt.Sprintf("%f", startSeconds),
		"-t", fmt.Sprintf("%f", duration),
		outputPath,
	)
	
	return cmd.Run()
}

func transcribeChunk(chunkPath, apiURL, apiKey string) (string, error) {
	// Create a buffer to store our request body as bytes
	var requestBody bytes.Buffer
	
	// Create a multipart writer
	multipartWriter := multipart.NewWriter(&requestBody)
	
	// Add the file
	fileWriter, err := multipartWriter.CreateFormFile("file", "chunk.flac")
	if err != nil {
		return "", err
	}
	
	// Open the file
	file, err := os.Open(chunkPath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	
	// Copy the file data to the form
	if _, err = io.Copy(fileWriter, file); err != nil {
		return "", err
	}
	
	// Add other form fields
	if err = multipartWriter.WriteField("model", "distil-whisper-large-v3-en"); err != nil {
		return "", err
	}
	if err = multipartWriter.WriteField("temperature", "0"); err != nil {
		return "", err
	}
	if err = multipartWriter.WriteField("response_format", "verbose_json"); err != nil {
		return "", err
	}
	if err = multipartWriter.WriteField("language", "en"); err != nil {
		return "", err
	}
	
	// Close the multipart writer to set the terminating boundary
	if err = multipartWriter.Close(); err != nil {
		return "", err
	}
	
	// Create the request
	req, err := http.NewRequest("POST", apiURL, &requestBody)
	if err != nil {
		return "", err
	}
	
	// Set headers
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+apiKey)
	
	// Set timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	
	// Make the request
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	// Check status code
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API returned non-200 status: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}
	
	// Parse response
	var result TranscriptionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	
	return result.Text, nil
}

func deleteFiles(files []string) {
	for _, file := range files {
		if err := os.Remove(file); err != nil {
			log.Printf("Error deleting file %s: %v", file, err)
		}
	}
}