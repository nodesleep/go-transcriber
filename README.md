# Audio Transcription API

A Go (Golang) backend service for transcribing audio files using FFmpeg for processing and the Groq API for transcription.

## Features

- Upload and transcribe audio files (MP3, WAV, FLAC, M4A, etc.)
- Audio preprocessing with FFmpeg for optimal transcription quality
- Audio chunking for large files to improve transcription accuracy
- Parallel processing of audio chunks for faster results
- RESTful API for easy integration with frontend applications

## Tech Stack

- Go (Golang)
- Gin framework for HTTP routing and middleware
- FFmpeg for audio processing
- Groq API for transcription (using the Whisper model)
- UUID generation for temporary file management

## Requirements

- Go 1.16+
- FFmpeg installed on the system
- FFprobe installed on the system (comes with FFmpeg)
- Groq API key

## Installation

1. Clone the repository:

   ```bash
   git clone <repository-url>
   cd <repository-directory>
   ```

2. Install dependencies:

   ```bash
   go mod download
   ```

3. Add your Groq API key to the `transcribeAudio` function in `main.go`:
   ```go
   // Replace the empty string with your API key
   apiKey := "your_groq_api_key_here"
   ```

## Running the Server

Start the server:

```bash
go run main.go
```

The server will run on port 8080 by default.

## API Endpoints

### Transcribe Audio

**Endpoint:** `POST /api/transcribe`

**Request:**

- Content-Type: `multipart/form-data`
- Body:
  - `file`: Audio file (MP3, WAV, FLAC, M4A, etc.)

**Response:**

```json
{
  "transcription": "This is the transcribed text from the audio file..."
}
```

**Error Response:**

```json
{
  "error": "Error message describing what went wrong"
}
```

## Implementation Details

### Audio Processing Pipeline

1. **File Upload**: The API accepts audio file uploads via a multipart form.
2. **Preprocessing**: The uploaded audio is preprocessed using FFmpeg:
   - Converted to 16kHz sample rate
   - Reduced to mono channel
   - Converted to FLAC format for optimal transcription
3. **Chunking**: Large audio files are split into manageable chunks (2 minutes each with a 1-second overlap)
4. **Parallel Processing**: Multiple chunks are transcribed simultaneously (limited to 5 concurrent operations)
5. **Transcription**: Each chunk is sent to the Groq API for transcription using the `distil-whisper-large-v3-en` model
6. **Combination**: Results are combined and returned as a complete transcription

### Code Structure

- **Main Function**: Sets up the Gin router with CORS configuration and defines the API routes
- **transcribeAudio**: The main handler function that orchestrates the audio processing and transcription
- **preprocessAudioFile**: Processes audio files to prepare them for transcription
- **getAudioChunkData**: Analyzes audio files to determine chunking parameters
- **chunkifyAudioFile**: Splits large audio files into smaller chunks
- **createAudioChunkFile**: Creates individual audio chunk files
- **transcribeChunk**: Sends audio chunks to the Groq API for transcription
- **deleteFiles**: Cleans up temporary files

## Extending the API

To add additional functionality:

1. Add new route handlers in `main.go`
2. Create helper functions for new features
3. Update the error handling as needed

## CORS Configuration

The API is configured to accept requests from:

- `http://localhost:5173` (default Vue.js development server)

If you need to allow additional origins, update the CORS configuration in the `main` function.

## Error Handling

The API includes comprehensive error handling:

- Validation errors for missing files or bad requests
- Audio processing errors from FFmpeg operations
- Transcription errors from the Groq API
- Cleanup of temporary files even in error cases

## Performance Considerations

- **Concurrency Control**: The API limits the number of concurrent transcription operations to 5 to prevent overloading the system or hitting API rate limits.
- **CPU Utilization**: Audio chunk processing uses the available CPU cores (with a default of 4 if GOMAXPROCS is not set)
- **Temporary File Management**: All temporary files are properly cleaned up after processing.

## License

[MIT License](LICENSE)
