// cmd/wa/media.go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var mediaOutput string

var mediaCmd = &cobra.Command{
	Use:   "media",
	Short: "Media operations",
}

var mediaDownloadCmd = &cobra.Command{
	Use:   "download <message-id>",
	Short: "Download media from a message",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}

		data, mimeType, err := c.DownloadMedia(args[0])
		if err != nil {
			exitError(err.Error(), 1)
		}

		outPath := mediaOutput
		if outPath == "" {
			// Default: message-id with extension based on MIME
			ext := mimeToExt(mimeType)
			outPath = args[0] + ext
		}

		if err := os.WriteFile(outPath, data, 0644); err != nil {
			exitError(fmt.Sprintf("writing file: %v", err), 1)
		}
		fmt.Printf("Downloaded to %s (%d bytes)\n", outPath, len(data))
	},
}

func mimeToExt(mime string) string {
	m := map[string]string{
		"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif",
		"image/webp": ".webp", "video/mp4": ".mp4", "audio/mpeg": ".mp3",
		"audio/ogg": ".ogg", "audio/mp4": ".m4a", "application/pdf": ".pdf",
	}
	if ext, ok := m[mime]; ok {
		return ext
	}
	return ".bin"
}

func init() {
	mediaDownloadCmd.Flags().StringVarP(&mediaOutput, "output", "o", "", "output file path")
	mediaCmd.AddCommand(mediaDownloadCmd)
	rootCmd.AddCommand(mediaCmd)
}
