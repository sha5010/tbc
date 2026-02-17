package terabox

import (
	"context"
	"crypto/rc4"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path"
	"tbc/internal/util"
	"time"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"golang.org/x/sync/errgroup"
)

type DownloadOptions struct {
	DownloadChunkSize int64
	MaxConcurrency    int64
}

type homeInfo struct {
	Errno  int    `json:"errno"`
	Errmsg string `json:"errmsg"`
	Data   struct {
		Username  string `json:"username"`
		Uk        string `json:"uk"`
		Sign1     string `json:"sign1"`
		Timestamp int    `json:"timestamp"`
		Sign3     string `json:"sign3"`
	} `json:"data"`
}

type downloadInfo struct {
	Errno int `json:"errno"`
	Dlink []struct {
		FsID  string `json:"fs_id"`
		Dlink string `json:"dlink"`
	} `json:"dlink"`
	FileInfo struct {
		Size     int    `json:"size"`
		Filename string `json:"filename"`
	} `json:"file_info"`
}

type DownloadLink struct {
	Dlink    string
	FileName string
	FileSize int
}

func (c *Client) getHomeInfo() (*homeInfo, error) {
	body, err := util.GetResponse(c.client.R().
		SetQueryParams(map[string]string{
			"app_id":  appId,
			"jsToken": c.jsToken,
		}).
		Get("/api/home/info"))
	if err != nil {
		return nil, fmt.Errorf("failed to get list: %w", err)
	}

	var info homeInfo
	json.Unmarshal(body, &info)

	return &info, nil
}

func (c *Client) GetDownloadLink(remotePath string) (*DownloadLink, error) {
	remotePath = util.GetAbsPath(c.cwd, remotePath)
	if remotePath == "/" {
		return nil, fmt.Errorf("cannot download root directory")
	}

	info, err := c.getHomeInfo()
	if err != nil {
		return nil, err
	}

	sign1 := info.Data.Sign1
	sign3 := info.Data.Sign3

	// calculate RC4 (plaintext: sign1, key: sign3)
	cipher, _ := rc4.NewCipher([]byte(sign3))
	sign1Bytes := []byte(sign1)
	ct := make([]byte, len(sign1Bytes))
	cipher.XORKeyStream(ct, sign1Bytes)

	sign := base64.StdEncoding.EncodeToString(ct)

	files, err := c.List(ListOptions{RemoteDir: path.Dir(remotePath)})
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no files found")
	}

	var fileId uint64
	for _, file := range files {
		if path.Base(file.Name) == path.Base(remotePath) {
			fileId = file.FileId
			break
		}
	}

	body, err := util.GetResponse(c.client.R().
		SetQueryParams(map[string]string{
			"app_id":     appId,
			"jsToken":    c.jsToken,
			"bdstoken":   c.bdsToken,
			"need_speed": "0",
			"fidlist":    fmt.Sprintf("[%d]", fileId),
			"type":       "dlink",
			"vip":        "2",
			"sign":       sign,
			"timestamp":  fmt.Sprintf("%d", info.Data.Timestamp),
		}).
		Get("/api/download"))

	var dInfo downloadInfo
	json.Unmarshal(body, &dInfo)

	if dInfo.Errno != 0 {
		return nil, fmt.Errorf("failed to get download link (errno: %d)", dInfo.Errno)
	}

	if len(dInfo.Dlink) == 0 {
		return nil, fmt.Errorf("no download link found in response")
	}

	fileSize := dInfo.FileInfo.Size
	fileName := dInfo.FileInfo.Filename

	return &DownloadLink{
		Dlink:    dInfo.Dlink[0].Dlink,
		FileSize: fileSize,
		FileName: fileName,
	}, nil
}

type progressWriter struct {
	totalBar *mpb.Bar
	chunkBar *mpb.Bar
	writer   io.Writer
	lastTime time.Time
}

type offsetWriter struct {
	file   *os.File
	offset int64
}

func (w *offsetWriter) Write(p []byte) (n int, err error) {
	n, err = w.file.WriteAt(p, w.offset)
	w.offset += int64(n)
	return
}

func (pw *progressWriter) Write(p []byte) (n int, err error) {
	n, err = pw.writer.Write(p)
	if pw.totalBar != nil {
		if pw.lastTime.Unix() > 0 {
			pw.totalBar.EwmaIncrBy(n, time.Since(pw.lastTime))
		} else {
			pw.totalBar.IncrBy(n)
		}
	}
	if pw.chunkBar != nil {
		if pw.lastTime.Unix() > 0 {
			pw.chunkBar.EwmaIncrBy(n, time.Since(pw.lastTime))
		} else {
			pw.chunkBar.IncrBy(n)
		}
	}
	pw.lastTime = time.Now()
	return
}

type DownloadFile struct {
	RemotePath string
	LocalDir   string
}

func (c *Client) Download(ctx context.Context, downloadFiles []DownloadFile, receiver <-chan DownloadFile, opts *DownloadOptions) error {
	if downloadFiles == nil && receiver == nil {
		return fmt.Errorf("must specify one of downloadFiles or receiver")
	}

	defer log.SetOutput(os.Stderr)
	log.SetOutput(io.Discard)

	p := mpb.NewWithContext(ctx,
		mpb.WithOutput(os.Stderr),
		mpb.WithWidth(25),
		mpb.WithRefreshRate(180*time.Millisecond),
	)

	downloadChunkSize := opts.DownloadChunkSize
	downloadConcurrency := opts.MaxConcurrency

	recvChan := receiver
	if recvChan == nil {
		listChan := make(chan DownloadFile, len(downloadFiles))
		for _, downloadFile := range downloadFiles {
			listChan <- downloadFile
		}
		close(listChan)
		recvChan = listChan
	}

	// Create a custom HTTP client for downloading chunks
	// Disable keep-alives to prevent "Unsolicited response" errors on idle connections
	httpClient := &http.Client{
		Transport: &http.Transport{
			Proxy:             http.ProxyFromEnvironment,
			DisableKeepAlives: true,
		},
	}

	fileCnt := 0
	for downloadFile := range recvChan {
		remotePath := downloadFile.RemotePath
		outputDir := downloadFile.LocalDir

		dLink, err := c.GetDownloadLink(remotePath)
		if err != nil {
			return err
		}

		dirInfo, err := os.Stat(outputDir)
		if err != nil {
			return err
		}
		if !dirInfo.IsDir() {
			return fmt.Errorf("Specified output directory is not a directory")
		}
		fileSize := int64(dLink.FileSize)
		fileName := dLink.FileName
		outputPath := path.Join(outputDir, fileName)

		file, err := os.OpenFile(outputPath, os.O_CREATE | os.O_RDWR, 0o666)
		if err != nil {
			return err
		}
		defer file.Close()

		if fileSize > downloadChunkSize {
			if err := file.Truncate(fileSize); err != nil {
				return err
			}
		}

		var numChunks int64 = 1
		if downloadConcurrency > 1 {
			numChunks = (fileSize + downloadChunkSize - 1) / downloadChunkSize
		}
		chunkSize := int64(math.Ceil(float64(fileSize) / float64(numChunks)))

		var incompleteChunks []int64 = make([]int64, 0, numChunks)
		var alreadyDownloadedChunks = int64(0)
		resumeFile, err := os.OpenFile(outputPath + ".resume", os.O_CREATE | os.O_RDWR, 0o666)
		if err != nil {
			return err
		}
		defer resumeFile.Close()
		resumeFileInfo, err := resumeFile.Stat()
		if err != nil {
			return err
		}
		if resumeFileInfo.Size() == numChunks {
			data, err := io.ReadAll(resumeFile)
			if err != nil {
				return err
			}

			for i, b := range data {
				if b == 0 {
					incompleteChunks = append(incompleteChunks, int64(i))
				} else {
					alreadyDownloadedChunks++
				}
			}
		} else {
			resumeFile.Truncate(0)
			resumeFile.Truncate(numChunks)
			for i := int64(0); i < numChunks; i++ {
				incompleteChunks = append(incompleteChunks, i)
			}
		}

		fileCnt += 1
		totalBar, _ := p.Add(fileSize - alreadyDownloadedChunks * chunkSize,
			mpb.BarStyle().Lbound("[").Filler("=").Tip(">").Padding("-").Rbound("]").Build(),
			mpb.BarPriority(fileCnt*1000),
			mpb.PrependDecorators(
				decor.OnCompleteOrOnAbort(decor.Spinner(nil, decor.WCSyncSpaceR), "✓"),
				decor.Name(fmt.Sprintf("%s ", fileName), decor.WCSyncSpaceR),
				decor.NewPercentage("%.1f", decor.WCSyncSpace),
				decor.Name(" ", decor.WCSyncWidthR),
			),
			mpb.AppendDecorators(
				decor.CountersKibiByte("%.2f / %.2f ", decor.WCSyncSpace),
				decor.AverageSpeed(decor.SizeB1024(0), "% .2f ", decor.WCSyncSpace),
				decor.OnComplete(decor.Name("ETA", decor.WCSyncSpace), ""),
				decor.OnComplete(decor.AverageETA(decor.ET_STYLE_GO, decor.WCSyncSpace), ""),
			),
		)

		sem := make(chan struct{}, downloadConcurrency)
		g, ctx := errgroup.WithContext(ctx)

		for _, chunkNo := range incompleteChunks {
			chunkNo := chunkNo
			start := chunkNo * chunkSize
			end := start + chunkSize - 1
			if end >= fileSize {
				end = fileSize - 1
			}
			barPriority := fileCnt*1000 + int(chunkNo)

			sem <- struct{}{}
			g.Go(func() error {
				defer func() { <-sem }()

				var chunkBar *mpb.Bar = nil
				if numChunks > 1 {
					chunkBar, _ = p.Add(end-start+1,
						mpb.BarStyle().Lbound("[").Filler("=").Tip(">").Padding("-").Rbound("]").Build(),
						mpb.BarPriority(barPriority),
						mpb.BarRemoveOnComplete(),
						mpb.PrependDecorators(
							decor.Name("\033[2m"),
							decor.Name("", decor.WCSyncSpaceR),
							decor.Name(fmt.Sprintf("  Chunk #%d", chunkNo), decor.WCSyncSpaceR),
							decor.NewPercentage("%.0f", decor.WCSyncSpace),
							decor.Name(" ", decor.WCSyncWidthR),
						),
						mpb.AppendDecorators(
							decor.CountersKibiByte("%.2f / %.2f ", decor.WCSyncSpace),
							decor.AverageSpeed(decor.SizeB1024(0), "% .1f ", decor.WCSyncSpace),
							decor.Name("\033[0m"),
						),
					)
				}

				req, err := http.NewRequest("GET", dLink.Dlink, nil)
				if err != nil {
					return fmt.Errorf("Error creating request: %w", err)
				}
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
				req.Header.Set("User-Agent", userAgent)
				req.Header.Set("Referer", baseUrl)
				req.Close = true // Ensure connection is closed
				for _, cookie := range c.cookies {
					req.AddCookie(cookie)
				}

				resp, err := httpClient.Do(req.WithContext(ctx))
				if err != nil {
					return fmt.Errorf("Failed to send request: %w", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
					return fmt.Errorf("Invalid status code: %d", resp.StatusCode)
				}

				// sometimes we see errors such as this, with successful status codes:
				// {"errno":424629,"errmsg":"need verify","request_id":9145275833161802533}
				// and that's why we check for short-reads

				if end-start+1 != resp.ContentLength {
					return fmt.Errorf("Invalid request payload size: %d (expected %d), status: %d", resp.ContentLength, end-start+1, resp.StatusCode)
				}

				pw := &progressWriter{
					writer:   &offsetWriter{file: file, offset: start},
					totalBar: totalBar,
					chunkBar: chunkBar,
				}

				if _, err := io.Copy(pw, resp.Body); err != nil {
					return fmt.Errorf("Failed to write response: %w", err)
				}

				resumeFile.WriteAt([]byte{1}, chunkNo)
				resumeFile.Sync()
				return nil
			})
		}

		if err := g.Wait(); err != nil {
			return err
		}
	}

	p.Wait()
	return nil
}
