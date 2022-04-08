package talebook

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bibliolater/bookhunter/pkg/log"
	"github.com/bibliolater/bookhunter/pkg/progress"
	"github.com/bibliolater/bookhunter/pkg/rename"
	"github.com/bibliolater/bookhunter/pkg/spider"
)

// downloadWorker is the download instance.
type downloadWorker struct {
	website      string
	progress     *progress.Progress
	client       *spider.Client
	userAgent    string
	retry        int
	downloadPath string
	formats      []string
	rename       bool
}

// Download would start download books from given website.
func (worker *downloadWorker) Download() {
	log.Infof("Start to download book.")

	// Try to acquire book ID from storage.
	for bookID := worker.progress.AcquireBookID(); bookID != progress.NoBookToDownload; bookID = worker.progress.AcquireBookID() {
		// Acquire book info.
		var info *BookResponse
		for i := 0; i < worker.retry; i++ {
			var err error

			info, err = worker.queryBookInfo(bookID)
			if err == nil {
				break
			}

			// Log the error after last try.
			if i == worker.retry-1 {
				log.Fatal(err)
			}
		}

		if info == nil {
			log.Warnf("[%d/%d] Book with ID %d is not exist on target website.", bookID, worker.progress.Size(), bookID)
			worker.downloadedBook(bookID)
			continue
		}

		// Find formats to download.
		for _, file := range info.Book.Files {
			for i := 0; i < worker.retry; i++ {
				var err error

				err = worker.downloadBook(bookID, info.Book.Title, file.Format, file.Href)
				if err == nil {
					break
				}

				// Log the error after last try.
				if i == worker.retry-1 {
					log.Fatal(err)
				}
			}
		}

		worker.downloadedBook(bookID)
	}
}

// downloadedBook would record the download statue into storage.
func (worker *downloadWorker) downloadedBook(bookID int64) {
	if err := worker.progress.SaveBookID(bookID); err != nil {
		log.Fatal(err)
	}
}

// downloadBook will download the book file from
func (worker *downloadWorker) downloadBook(bookID int64, title, format, href string) error {
	valid := false
	for _, f := range worker.formats {
		if f == format {
			valid = true
			break
		}
	}

	if !valid {
		// Skip this format.
		return nil
	}

	// Start download.
	site := ""
	if strings.HasPrefix(href, "http") {
		// Backward API support.
		site = href
	} else {
		site = spider.GenerateUrl(worker.website, href)
	}

	resp, err := worker.client.Get(site, "")
	if err != nil {
		return spider.WrapTimeOut(err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Generate file name.
	filename := strconv.FormatInt(bookID, 10) + "." + strings.ToLower(format)
	// Use readable name.
	if !worker.rename {
		name := spider.Filename(resp)
		if name == "" {
			filename = title + "." + strings.ToLower(format)
		} else {
			filename = name
		}
	}

	// Remove illegal characters. Ref: https://en.wikipedia.org/wiki/Filename#Reserved_characters_and_words
	filename = rename.EscapeFilename(filename)

	// Generate the file path.
	file := filepath.Join(worker.downloadPath, filename)

	// Remove the exist file.
	if _, err := os.Stat(file); err == nil {
		if err := os.Remove(file); err != nil {
			return err
		}
	}

	// Create file writer.
	writer, err := os.Create(file)
	if err != nil {
		return err
	}
	defer func() { _ = writer.Close() }()

	// Add download progress
	bar := log.NewProgressBar(bookID, worker.lastBookID(), format+" "+title, resp.ContentLength)

	// Write file content
	_, err = io.Copy(io.MultiWriter(writer, bar), resp.Body)
	if err != nil {
		return spider.WrapTimeOut(err)
	}

	return nil
}

// queryBookInfo will find the required book information.
func (worker *downloadWorker) queryBookInfo(bookID int64) (*BookResponse, error) {
	site := spider.GenerateUrl(worker.website, "/api/book", strconv.FormatInt(bookID, 10))

	resp, err := worker.client.Get(site, "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	result := &BookResponse{}
	if err := spider.DecodeResponse(resp, result); err != nil {
		return nil, err
	}

	switch result.Err {
	case SuccessStatus:
		return result, nil
	case BookNotFoundStatus:
		return nil, nil
	default:
		return nil, errors.New(result.Msg)
	}
}

// lastBookID will return the last book's ID
func (worker *downloadWorker) lastBookID() int64 {
	return worker.progress.Size()
}
