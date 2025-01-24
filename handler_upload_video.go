package main

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const maxMemory = 1 << 30 //10GB
	r.Body = http.MaxBytesReader(w, r.Body, int64(maxMemory))

	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "VideoID is not provided", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't find video", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn'% find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Not authorized to update this video", err)
		return
	}

	videoSrc, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Getting video failed", err)
		return
	}
	defer videoSrc.Close()

	mediaType := header.Header.Get("Content-Type")
	if mediaType == "" {
		respondWithError(w, http.StatusBadRequest, "Missing Content-Type for video", nil)
		return
	}
	mediaType, _, err = mime.ParseMediaType(mediaType)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to parse media type", err)
	}
	switch mediaType {
	case "video/mp4":
		break
	default:
		respondWithError(w, http.StatusBadRequest, "Not supported format", nil)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "created tempFile failed", nil)
		return
	}
	defer tempFile.Close()

	if _, err = io.Copy(tempFile, videoSrc); err != nil {
		respondWithError(w, http.StatusInternalServerError, "copy data to file failed", err)
		return
	}

	if _, err = tempFile.Seek(0, io.SeekStart); err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to reset video file ", err)
		return
	}
	processedVideoPath, err := processVideoForStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "created processVideo failed", err)
	}
	fmt.Println("ProcessVideoPath: ", processedVideoPath)
	os.Remove(tempFile.Name())
	defer os.Remove(processedVideoPath)
	ratio, err := getVideoAspectRatio(processedVideoPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to get vidoe ratio ", err)
		return
	}

	key := getAssetPathWithPrefix(mediaType, ratio)
	videoFile, err := os.Open(processedVideoPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to open vidoe file", err)
		return
	}
	defer videoFile.Close()

	_, err = cfg.s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(key),
		Body:        videoFile,
		ContentType: aws.String(mediaType),
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to upload video to s3Bucket", err)
		return
	}

	videoURL := cfg.getObjectURL(key)
	video.VideoURL = &videoURL
	fmt.Println("VideoURL: ", videoURL)
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Get video from database failed", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}
