package main

import (
	//"context"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/zdhxmo/radical/config"

	gogpt "github.com/sashabaranov/go-gpt3"

	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
)

var new_uuid = uuid.New()
var directory = "./data/" + new_uuid.String()

var (
    configs config.Config
    wg sync.WaitGroup
    imageName string
    text2speechName string
    timestampFile string
    timestampSRT string
    timestampASS string
    videoAudioFileName string
    finalVideoName string
    convertName string
)

func main() {
    configs, _ = config.LoadConfig(".")

    r := gin.Default()
    r.LoadHTMLGlob("./ui/html/**")
    r.Static("/static", "ui/static")

    videoPath := directory + "/10_FINAL_VIDEO.mp4"
    r.Static(videoPath, directory)

    r.GET("/", homePage)
    r.POST("/execute-query", openAI_APICall)

    r.GET("/select-text", selectText)
    r.POST("/create-video", createVideo)

    r.GET("/video", showVideo)

    log.Fatal(r.Run(":8080"))
}



func homePage(c *gin.Context) {
    c.HTML(http.StatusOK, "home.page.tmpl", nil)
}



func openAI_APICall(c *gin.Context) {
    query := c.PostForm("query")
    image, err := c.FormFile("image")
    if err != nil {
        c.String(http.StatusBadRequest, fmt.Sprintf("get form err: %s", err.Error()))
        return
    }
    filename := image.Filename

    // create a destination path to save the file
    dst := directory + "/01_" + filename
    imageName = dst

    if _, err := os.Stat(directory); os.IsNotExist(err) {
        os.MkdirAll(directory, os.ModePerm)
    }

    // Save the file to the specified directory
    if err := c.SaveUploadedFile(image, dst); err != nil {
        log.Fatal(err)
    } 

    openai_apiKey := configs.OpenAIKey
    prompt_prefix := "generate 3 distinct paragraph texts about: "
    prompt := prompt_prefix + query

    done := <- textCompletion(prompt, c, openai_apiKey, directory)

    if done {
        c.Redirect(http.StatusSeeOther, "/select-text") 
    }
}

func textCompletion(prompt string, ctx context.Context, openai_apiKey string, directory string) chan bool {
    c := gogpt.NewClient(openai_apiKey)
    
    req := gogpt.CompletionRequest{
        Model: gogpt.GPT3TextDavinci001,
        Prompt: prompt,
        MaxTokens: 1024,
        N: 1,
        Temperature: 0.5,
    }

    responseChan := make(chan bool)

  // Start a goroutine to handle the API call
    go func() {
        resp, err := c.CreateCompletion(ctx, req)
        if err != nil {
            log.Fatal(err)
        }

        // Create the directory
        os.Mkdir(directory, os.ModePerm)

        file := directory + "/02_chatGPT_outputs.txt"

        text := resp.Choices[0].Text
        if err := ioutil.WriteFile(file, []byte(text), 0644); err != nil {
            log.Fatal(err)
        }                        
        fmt.Print("prompt request success\n")

        // Send the response to the channel
        responseChan <- true
    }()

    fmt.Print("prompt request success\n")
    
    return responseChan
}

func selectText(c *gin.Context) {
    chat_output := directory + "/02_chatGPT_outputs.txt"

    // Read the text file
    data, err := ioutil.ReadFile(chat_output)
    if err != nil {
        log.Fatal(err)
    }

    re := regexp.MustCompile(`[0-9].`)
    paragraphs := re.Split(string(data), -1)


    for i, p := range paragraphs {
        paragraphs[i] = strings.TrimSpace(p)
    }

    paragraphs = paragraphs[1:]
    
    // Pass the paragraphs to the template
    c.HTML(http.StatusOK, "select.page.tmpl", gin.H{
        "option1": paragraphs[0],
        "option2": paragraphs[1],
        "option3": paragraphs[2],
    })
}

func createVideo(c *gin.Context) {
    // raio button selection
    final_query := c.PostForm("response")

    // Create a channel to wait for the video to be created
    videoDone := make(chan bool)

    // Spawn a goroutine to create the video
    go func() {
        // text to speech the chatgpt output
        file := directory + "/03_text2speech.wav" 
        text2speechName = file

        outputFile := flag.String("output-file", file, "The name of the output file.")
        flag.Parse()
    
        err := synthesizeText(os.Stdout, final_query, *outputFile)
        if err != nil {
            log.Fatal(err)
        }

        // get video of color screen  -- length of text audio 
        createVideoFromImage()
    
        // overlay tts audio on video
        compositeAudioVideo()

        // read audio track
        upload_audio_file()

        // parse json and convert to usable form - ass
        parseJson()

        // overlay text on video and composite
        composite()

        // Signal that the video is done
        videoDone <- true
    }()

    // Wait for the video to be created before redirecting
    <-videoDone

    c.Redirect(http.StatusSeeOther, "/video")
}


// SynthesizeText synthesizes plain text and saves the output to outputFile.
func synthesizeText(w io.Writer, text, outputFile string) error {
    ctx := context.Background() 

    client, err := texttospeech.NewClient(ctx)
    if err != nil {
        return err
    }
    defer client.Close()

    req := texttospeechpb.SynthesizeSpeechRequest{
        Input: &texttospeechpb.SynthesisInput{
            InputSource: &texttospeechpb.SynthesisInput_Text{Text: text},
        },
        // Note: the voice can also be specified by name.
        // Names of voices can be retrieved with client.ListVoices().
        Voice: &texttospeechpb.VoiceSelectionParams{
            LanguageCode: "en-US",
            SsmlGender:   texttospeechpb.SsmlVoiceGender_FEMALE,
        },
        AudioConfig: &texttospeechpb.AudioConfig{
            AudioEncoding: texttospeechpb.AudioEncoding_ALAW,
        },
    }

    resp, err := client.SynthesizeSpeech(ctx, &req)
    if err != nil {
        return err
    }

    err = ioutil.WriteFile(outputFile, resp.AudioContent, 0644)
    if err != nil {
        return err
    }
    fmt.Fprintf(w, "Audio content written to file: %v\n", outputFile)
    return nil
}

func createVideoFromImage() {
    duration := findLengthOfAudioClip()
    durationStr := fmt.Sprintf("%v", duration)

    video_file := directory + "/04_color_video.mkv"
    videoAudioFileName = video_file

    // put static image together to create a video
    cmd := exec.Command("ffmpeg", "-loop", "1", "-i", imageName, "-c:v", "libx264", "-c:a", "libvorbis", "-pix_fmt", "yuv420p", "-vf", "scale=1080:1920", "-t", durationStr , "-f", "matroska", "-y", video_file)


    out, err := cmd.CombinedOutput()
    if err != nil {
        fmt.Println(fmt.Sprint(err) + ": " + string(out))
        return
    }

    fmt.Println("Video created successfully\n")
}

func findLengthOfAudioClip() float64 {
    // Define the input file path
    inputFile := text2speechName

    
 // Define the ffprobe command to get the duration of the audio file
    cmd := exec.Command("ffprobe", "-i", inputFile, "-show_entries", "format=duration", "-v", "quiet", "-of", "csv=p=0")

    // Run the command and capture the output
    output, err := cmd.CombinedOutput()
    if err != nil {
        fmt.Println(fmt.Sprint(err) + ": " + string(output))
    }

    // Convert the output to a float
    duration, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
    if err != nil {
        fmt.Println(err)
    }

    return duration
}

func compositeAudioVideo() error {
    audioFile := text2speechName
    videoFile := directory + "/04_color_video.mkv"
    outputFile := directory + "/05_video_audio.mkv"
    videoAudioFileName = outputFile

    cmd := exec.Command("ffmpeg", "-i", videoFile, "-i", audioFile, "-c:v", "copy", "-c:a", "aac", "-strict", "experimental", "-f", "matroska", "-y", outputFile)
    if output, err := cmd.CombinedOutput(); err != nil {
        return fmt.Errorf("cmd.Run() failed with %s\n", output)
    }

    fmt.Print("Image composited to a video \n")
    return nil
}


func upload_audio_file() {
    api_key := configs.AssemblyKey
    upload_url := "https://api.assemblyai.com/v2/upload"

    // Load file
    data, err := ioutil.ReadFile(text2speechName)
    if err != nil {
        log.Fatalln(err)
    }

    // Setup HTTP client and set header
    client := &http.Client{}
    req, _ := http.NewRequest("POST", upload_url, bytes.NewBuffer(data))
    req.Header.Set("authorization", api_key)
    res, err := client.Do(req)

    if err != nil {
        log.Fatalln(err)
    }

    // decode json and store it in a map
    var result map[string]interface{}
    json.NewDecoder(res.Body).Decode(&result)

    url := fmt.Sprintf("%v", result["upload_url"])

    fmt.Print("audio file uploaded\n")

    time.Sleep(time.Second)

    transcribe_audio(url, api_key)

}

func transcribe_audio(audio_url, api_key string) {
    transcript_url := "https://api.assemblyai.com/v2/transcript"

    // prepare json data
    values := map[string]string{"audio_url": audio_url}
    jsonData, _ := json.Marshal(values)

    // setup HTTP client and set header
    client := &http.Client{}
    req, _ := http.NewRequest("POST", transcript_url, bytes.NewBuffer(jsonData))
    req.Header.Set("content-type", "application/json")
    req.Header.Set("authorization", api_key)
    res, err := client.Do(req)
    if err != nil {
        log.Fatal(err)
    }

    defer res.Body.Close()

    // decode json and store it in a map
    var result map[string]interface{}
    json.NewDecoder(res.Body).Decode(&result)

    // print the id of the transcribed audio
    id := fmt.Sprintf("%v", result["id"])

    fmt.Print("audio file transcribed\n")

    polling_url := transcript_url + "/" + id

    time.Sleep(time.Second)

    poll(polling_url, api_key)
}

func poll(polling_url, api_key string) {

    for {
        fmt.Print("Polling the transcription")

        client := &http.Client{}
        req, _ := http.NewRequest("GET", polling_url, nil)
        req.Header.Set("content-type", "application/json")
        req.Header.Set("authorization", api_key)
        res, err := client.Do(req)
        if err != nil {
            log.Fatalln(err)
        }

        defer res.Body.Close()

        var result map[string]interface{}
        json.NewDecoder(res.Body).Decode(&result)

        if result["status"] == "completed" {
            words := result["words"]

            wordsJSON, _ := json.Marshal(words)

            timestampFile = directory + "/06_timestamped_text.json"
            ioutil.WriteFile(timestampFile, wordsJSON, 0644)

            fmt.Print("timestamp file generated\n")
            break
        }
        time.Sleep(time.Second)
    }

}

type TextSegment struct {
        Start       int         `json:"start"`
        End         int         `json:"end"`
        Text        string      `json:"text"`
}

func parseJson() {
    input_json := timestampFile
    content, err := ioutil.ReadFile(input_json)
    if err!= nil {
        log.Fatal(err)
    }

    // unmarshal the json
    var segments []TextSegment
    err = json.Unmarshal(content, &segments)
    if err != nil {
        log.Fatal(err)
    }

    timestampSRT = directory + "/07_timestamped_text.srt"

    file, err := os.Create(timestampSRT)
    if err != nil {
        log.Fatal(err)
    }

    defer file.Close()

    for i, segment := range segments {
        startTime := time.Duration(segment.Start) * time.Millisecond
        endTime := time.Duration(segment.End) * time.Millisecond

        zeroTime := time.Time{}

        startTimeStamp := zeroTime.Add(startTime)
        endTimeStamp := zeroTime.Add(endTime)

        startTimeStr := startTimeStamp.Format("15:04:05,00")
        endTimeStr := endTimeStamp.Format("15:04:05,00")

        count := strconv.Itoa(i + 1)

        file.WriteString(count + "\n" + startTimeStr + " --> " + endTimeStr + "\n" + segment.Text + "\n\n")
    }

    editJSON()
}

type subtitle struct {
    startTime string
    endTime   string
    text      string
}

func editJSON() {
    // Open the SRT file
    file, err := os.Open(timestampSRT)
    if err != nil {
        fmt.Println(err)
        return
    }
    defer file.Close()

    // Create a new scanner to read the file
    scanner := bufio.NewScanner(file)

    // Create a slice to store the grouped subtitles
    var groupedSubtitles []subtitle

    // Initialize the counter variable
    counter := 0

    // Initialize a variable to store the start and end timestamps
    var startTime, endTime string

    // Initialize a variable to store the text of the subtitles
    var text string

    // Read the file line by line
    for scanner.Scan() {
        line := scanner.Text()

        // Check if the line is a subtitle number
        _, err := strconv.Atoi(line)
        if err == nil {
            counter++
            continue
        }

        // Check if the line is a timestamp
        if strings.Contains(line, "-->") {
            // Split the line by "-->" to get the start and end timestamps
            timestamps := strings.Split(line, " --> ")
            if counter == 1 {
                startTime = timestamps[0]
            }

            endTime = timestamps[1]
            continue
        }

        // If the line is not a subtitle number or timestamp, it is the subtitle text
        text += line + " "

        // If counter is equal to 4, group the first four subtitles are strung together
        if counter == 4 {
            groupedSubtitles = append(groupedSubtitles, subtitle{startTime, endTime, text})
            counter = 0
            text = ""
        }
    }

    timestampSRT = directory + "/08_modified_timestamped_text.srt"
    file, err = os.Create(timestampSRT)
    if err != nil {
        log.Fatal(err)
    }
    // Print the grouped subtitles
    for i, groupedSub := range groupedSubtitles {
        count := strconv.Itoa(i + 1)

        file.WriteString(count + "\n" + groupedSub.startTime + " --> " + groupedSub.endTime + "\n" + groupedSub.text + "\n\n")
    }

    convertSRTtoASS()
}

func convertSRTtoASS() {
    // Define the input and output file paths
    timestampASS = directory + "/09_timestamped_text.ass"

    // Define the ffmpeg command to convert the file
    cmd := exec.Command("ffmpeg", "-i", timestampSRT, "-c:s", "ass", timestampASS)

    // Run the command
    output, err := cmd.CombinedOutput()
    if err != nil {
        fmt.Println(fmt.Sprint(err) + ": " + string(output))
        return
    }

    fmt.Println("SRT file converted to ASS successfully")
}

type Overlay struct {
    Timestamp string
    Text      string
}

func composite() {

    //videoPath := directory + "/10_FINAL_VIDEO.mkv"

    videoPath := directory + "/10_FINAL_VIDEO.mp4"
    finalVideoName = videoPath

    // Define the ffmpeg command to overlay the text on the video
    cmd := exec.Command("ffmpeg", "-i", videoAudioFileName, "-vf", "subtitles=" + timestampASS + ":force_style='FontName=DejaVuSans,FontSize=10,Alignment=11,Outline=1,MarginV=1440,MarginR=30'", "-strict", "experimental", "-f", "mp4", videoPath)

    // Run the command
    output, err := cmd.CombinedOutput()
    if err != nil {
        fmt.Println(fmt.Sprint(err) + ": " + string(output))
        return
    }
    fmt.Println("Text overlayed on the video successfully")
}

func showVideo(c *gin.Context) {
    c.Header("Content-Type", "video/mp4")

    c.File(finalVideoName)

    /*
     *c.HTML(http.StatusOK, "video.page.tmpl", gin.H{
     *    "videoPath": finalVideoName,
     *})
     */
}
