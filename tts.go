// // Copyright 2019 Google LLC
// //
// // Licensed under the Apache License, Version 2.0 (the "License");
// // you may not use this file except in compliance with the License.
// // You may obtain a copy of the License at
// //
// //     https://www.apache.org/licenses/LICENSE-2.0
// //
// // Unless required by applicable law or agreed to in writing, software
// // distributed under the License is distributed on an "AS IS" BASIS,
// // WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// // See the License for the specific language governing permissions and
// // limitations under the License.
// // based on quickstart script at:
// // https://raw.githubusercontent.com/GoogleCloudPlatform/golang-samples/master/texttospeech/quickstart/quickstart.go
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"hash/crc32"
	"log"
	"time"

	"github.com/joeholley/jp/internal/gdrive"
	"github.com/joeholley/jp/internal/googleapis"
	"github.com/joeholley/jp/internal/gsheets"

	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	"google.golang.org/api/option"
	texttospeechpb "google.golang.org/genproto/googleapis/cloud/texttospeech/v1"
)

//var dir string
var dryrun bool
var ignoreChecksums bool
var inputsheetId string
var inputsheetRange string
var inputsheetName string
var csheetRange string

//var csheetId string
//var csheetName string
var lang string
var voicename string
var voicetype texttospeechpb.SsmlVoiceGender
var voicetypestr string
var mp3filenamesrange string
var foldername string

func init() {
	flag.StringVar(&inputsheetId, "sheetid", "1iKGLxmU83DBY_v6GBJHWssjLh_Mzqm9djFsYGNDc1IM", "Google Sheets Document ID. Found in URL")
	flag.StringVar(&inputsheetName, "sheetname", "Introduction", "which sheet in the spreadsheet (specified by ID) to read; Introduction is default")
	flag.StringVar(&inputsheetRange, "range", "C2", "sheets column to read for input to convert to speech (starting cell) C2 is default")
	flag.StringVar(&mp3filenamesrange, "filenamecolumn", "A2", "column used to label mp3 files.")
	flag.StringVar(&lang, "lang", "en-US", "language code for text to speech; en-US is default")
	flag.StringVar(&voicename, "voicename", "en-US-Wavenet-H", "name of the voice used; en-US-Wavenet-H is default")
	flag.StringVar(&voicetypestr, "voicetypestr", "female", "the type of voice for the audio; female is default")
	//flag.StringVar(&csheetName, "cname", "CheckSums", "which sheet 'tab' in the spreadsheet (specified by ID) to read/write checksums")
	flag.StringVar(&csheetRange, "crange", "L2", "sheets column to write checksums, for detecting changes to input")
	flag.StringVar(&foldername, "folder", "", "specify the folder name for the audio files to be created in drive (GPQ is default project)")
	//flag.StringVar(&csheetId, "csheetid", inputsheetId, "sheet ID to read/write checksums")
	flag.BoolVar(&dryrun, "dryrun", true, "Make no tts API calls, record no checksums, and output no files")
	flag.BoolVar(&ignoreChecksums, "ignorechecksums", false, "Make tts API calls even if it appears we have done it before based on checksums")
}

func main() {
	flag.Parse()
	crc32q := crc32.MakeTable(0xD5828281)
	var folderId []string

	// Instantiates a TTS client. This is separate from the google workspace client below.
	ctx := context.Background()
	client, err := texttospeech.NewClient(ctx,
		option.WithCredentialsFile("serviceaccount.json"))
	if err != nil {
		log.Fatal(err)
	}

	if voicetypestr == "female" {
		voicetype = texttospeechpb.SsmlVoiceGender_FEMALE
	} else if voicetypestr == "male" {
		voicetype = texttospeechpb.SsmlVoiceGender_MALE
	}

	// Set up the text-to-speech request on the text input with
	// voice parameters and audio file type.
	req := texttospeechpb.SynthesizeSpeechRequest{
		// Placeholder for the text input to be synthesized
		Input: &texttospeechpb.SynthesisInput{
			InputSource: &texttospeechpb.SynthesisInput_Text{Text: "no text specified"},
		},
		// Build the voice request, select the language code
		Voice: &texttospeechpb.VoiceSelectionParams{
			LanguageCode: lang,
			Name:         voicename,
			SsmlGender:   voicetype,
		},
		// Select mp3 audio encoding
		AudioConfig: &texttospeechpb.AudioConfig{
			AudioEncoding: texttospeechpb.AudioEncoding_MP3,
		},
	}

	// client for google workspace is separate creds to the TTS API above
	wsClient := googleapis.Client()
	fmt.Println("Initializing Google Sheets Client...")
	ssrv, err := gsheets.Service(wsClient)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Initializing Google Drive Client...")
	dsrv, err := gdrive.Service(wsClient)
	if err != nil {
		log.Fatal(err)
	}

	// Line-by-line read of input lines to translate
	// Format for readrange is "sheetTabName!FirstCell:ColName" to read an entire column regardless of length
	// Example "sheet1!A1:A"
	// This code assumes you're just reading one column, and that you want the whole column.
	readRange := fmt.Sprintf("%s!%s:%s", inputsheetName, inputsheetRange, string(inputsheetRange[0]))
	lines, err := gsheets.RetrieveCells(ssrv, inputsheetId, readRange)
	if err != nil {
		log.Fatal(err)
	}

	readFilenameRange := fmt.Sprintf("%s!%s:%s", inputsheetName, mp3filenamesrange, string(mp3filenamesrange[0]))
	mp3names, err := gsheets.RetrieveCells(ssrv, inputsheetId, readFilenameRange)
	if err != nil {
		log.Fatal(err)
	}

	// get checksums from previous runs (if any) from the google sheet.
	cRange := fmt.Sprintf("%s!%s:%s", inputsheetName, csheetRange, string(csheetRange[0]))
	cResults, err := gsheets.RetrieveCells(ssrv, inputsheetId, cRange)
	if err != nil {
		log.Fatal(err)
	}
	// Handle cases where there are more or fewer checksums than lines to convert to speech.
	// Make it so checksums and lines arrays are always the same length.
	checksums := make([]string, len(lines))
	for i, _ := range checksums {
		if i < len(lines) && i < len(cResults) {
			checksums[i] = cResults[i]
		}
	}

	for index, line := range lines {
		linelen := len(line)
		if linelen > 0 {

			// Truncate the line to print to screen during run, so it doesn't get hard to read.
			if linelen > 16 {
				linelen = 15
			}
			// Conversion to rune due to slicing non-ascii Japanese strings.
			// https://groups.google.com/g/golang-nuts/c/ZeYei0IWrLg/m/PfPnAy_TVsMJ
			truncLine := string([]rune(line[:linelen]))

			// Get checksum of current line
			curSum := fmt.Sprintf("%08x", crc32.Checksum([]byte(line), crc32q))

			// Log if we're forcing calls to API regardless of checksums
			if ignoreChecksums == true {
				fmt.Println(" '--ignoreChecksums' flag set; calling API regardless of checksum comparison results")
			}

			// If the checksums match, we've converted this string to speech in previous run, so skip
			// We already made sure the checksums and lines arrays are the same length, when we retrieved
			// the checksums from the spreadsheet.
			if ignoreChecksums == true || curSum != checksums[index] {
				mp3file := fmt.Sprintf("%s.mp3", mp3names[index])
				resp := &texttospeechpb.SynthesizeSpeechResponse{}

				// Replace the placeholder text to synthesize
				req.Input = &texttospeechpb.SynthesisInput{
					InputSource: &texttospeechpb.SynthesisInput_Text{Text: line},
				}

				if dryrun == true {
					fmt.Printf("DRY RUN: not actually writing ")
				} else {

					// Actual TTS API call
					resp, err = client.SynthesizeSpeech(ctx, &req)
					if err != nil {
						log.Fatal(err)
					}

					// Create folder based on current timestamp if we haven't already
					if len(folderId) == 0 {
						t := time.Now().Local()
						tstring := fmt.Sprintf("%d%02d%02d-%02d%02d", t.Year(), t.Month(), t.Day(),
							t.Hour(), t.Minute())
						if foldername == "" {
							folderId, err = gdrive.CreateFolder(dsrv, tstring)
						} else {
							folderId, err = gdrive.CreateFolder(dsrv, foldername)
						}

						if err != nil || len(folderId) == 0 {
							log.Fatal(err)
						}
					}

					// The resp's AudioContent is binary, use bytes IOReader to retrieve it
					mp3file := fmt.Sprintf("%s.mp3", mp3names[index])
					gdrive.CreateFile(dsrv, folderId, mp3file, bytes.NewReader(resp.AudioContent))
				}
				fmt.Printf("output audio file: %v\n", mp3file)

				// Update this checksum
				checksums[index] = curSum
			} else {
				fmt.Printf("Checksum of input string from previous run indicates %s hasn't changed, skipping\n", truncLine)
			}

		} else {
			fmt.Printf("%05d - empty line, nothing to do\n", index+1)
		}
	}
	// write the latest checksums back out to the specified sheet (overwrites values in this column!!)
	if dryrun == false {
		fmt.Printf("attempting to write checksums to sheet %s\n", inputsheetId)
		err = gsheets.WriteCells(ssrv, inputsheetId, cRange, checksums)
		if err != nil {
			log.Fatal(err)
		}
	}
}
