package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/flashmob/go-guerrilla"
	"github.com/flashmob/go-guerrilla/backends"
	"github.com/flashmob/go-guerrilla/log"
	"github.com/flashmob/go-guerrilla/mail"
)

const version = "1.0"

var Config SetupConfig

// SetupConfig - FILE - File used to specify the running config of the env for the process.
type SetupConfig struct {
	APIUrl string `json:"api_url"`
}

// MailMessage - SEND - Email File Data being Sent to the API Client
type MailMessage struct {
	ID           string       `json:"message_id"`
	Sender       string       `json:"sender"`
	From         string       `json:"from"`
	Recipients   []string     `json:"recipients"`
	To           []string     `json:"to"`
	CC           []string     `json:"cc"`
	BCC          []string     `json:"bcc"`
	Subject      string       `json:"subject"`
	Date         string       `json:"date"`
	Type         string       `json:"type"`
	HAttachments bool         `json:"has_attachments"`
	Attachments  []Attachment `json:"attachments"`
	Body         string       `json:"body"`
}

// Attachment - File Data that is in the Message that was removed and stoed as
//  an Object to be sent with the Message.
type Attachment struct {
	MIMEType string `json:"mime_type"`
	Name     string `json:"file_name"`
	Content  string `json:"content"`
}

func main() {
	fmt.Println("Starting: Go To Script")
	fmt.Println("Version: " + string(version))

	// Check for the config.json file.
	if _, err := os.Stat("config.json"); os.IsNotExist(err) {
		// Config File Does not Exist
		// Should never reach this statment.
		fmt.Println("No Config File! Setting default to be `http://localhost:80/report_email`")
		Config.APIUrl = "http://localhost:80/report_email"
	} else {
		// Read the File into jsonBlob then Unmarshal into runningConfig
		jsonBlob, _ := ioutil.ReadFile("config.json")
		err2 := json.Unmarshal(jsonBlob, &Config)
		if err2 != nil {
			fmt.Println("Loading config.json file Failed!")
			fmt.Println(err2)
			os.Exit(4)
		}
	}

	// Start Mailer Dameon from go-guerrilla
	cfg := &guerrilla.AppConfig{LogFile: log.OutputStdout.String()}

	sc := guerrilla.ServerConfig{
		ListenInterface: "0.0.0.0:25",
		IsEnabled:       true,
	}
	cfg.Servers = append(cfg.Servers, sc)
	bcfg := backends.BackendConfig{
		"save_workers_size":  3,
		"save_process":       "HeadersParser|Header|Debugger|toScript",
		"log_received_mails": true,
		"primary_mail_host":  "*",
		"wildcard_hosts":     "*",
	}
	cfg.BackendConfig = bcfg

	//WildConfig := wildcard_processor.WildcardConfig{}
	//WildConfig.WildcardHosts = "*"

	d := guerrilla.Daemon{Config: cfg}
	//d.AddProcessor("wildcard", wildcard_processor.WildcardProcessor)
	d.AddProcessor("toScript", parseEmail)

	err := d.Start()
	if err != nil {
		fmt.Println("Error Starting `go-guerrilla` server!", err)
		os.Exit(3)
	}

	for {
		// Keep the Process alive doing nothing while the Dameon is running.
	}

}

func parseEmail() backends.Decorator {
	// Respond to Store the Action
	return func(p backends.Processor) backends.Processor {
		// Return the Processing Actions
		return backends.ProcessWith(func(e *mail.Envelope, task backends.SelectTask) (backends.Result, error) {

			fmt.Println("New Email titled: " + e.Subject + " for " + strings.Join(AddressesToString(e.RcptTo), ", "))

			message := MailMessage{}
			message.Sender = e.Header.Get("Sender") + "(" + e.RemoteIP + ")"
			message.Date = e.Header.Get("Date")
			message.From = AddressToString(e.MailFrom)
			message.To = AddressesToString(e.RcptTo)
			message.CC = strings.Split(e.Header.Get("Cc"), ", ")
			message.BCC = strings.Split(e.Header.Get("Bcc"), ", ")
			message.Subject = e.Subject
			message.Body = e.Data.String()
			message.ID = e.Header.Get("Message-Id")
			message.Type = e.Header.Get("Content-Type")

			go postEmailData(message)

			// continue to the next Processor in the decorator chain
			return p.Process(e, task)
		})
	}
}

// AddressesToString - Convert []Address{User, Host} to []string
func AddressesToString(mIn []mail.Address) []string {
	var re []string
	for _, s := range mIn {
		re = append(re, AddressToString(s))
	}
	return re
}

// AddressToString - Convert Address{User, Host} to String
func AddressToString(m mail.Address) string {
	return m.User + "@" + m.Host
}

func postEmailData(msg MailMessage) {

	// Save Email Data to JSON and Prepair to send
	jsonStr, err := json.Marshal(msg)

	req, err := http.NewRequest("POST", Config.APIUrl, bytes.NewBuffer(jsonStr))
	if err != nil {
		fmt.Println("Failed to init NewRequest! Saving to local file!")
		fmt.Println(err)
		errW := ioutil.WriteFile("failed-"+string(time.Now().Unix())+"-report.txt", []byte(err.Error()), 0754)
		if errW != nil {
			fmt.Println("Error Writing Error")
			fmt.Println("> ", errW)
		}

		errT := ioutil.WriteFile("failed-"+string(time.Now().Unix())+"-email.json", jsonStr, 0754)
		if errT != nil {
			fmt.Println("Error Writing Email Backup")
			fmt.Println("> ", errW)
		}
	}

	req.Header.Set("User-Agent", fmt.Sprintf("GoToScript/%s", version))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Length", strconv.Itoa(len(jsonStr)))

	client := &http.Client{}
	resp, errR := client.Do(req)
	if errR != nil {
		fmt.Println("Failed to connect to the given URL. URL: " + Config.APIUrl)
		fmt.Println(errR)
	}
	defer resp.Body.Close()

	fmt.Println("Response Status: ", resp.Status)
	body, _ := ioutil.ReadAll(resp.Body)
	fmt.Println("Response Body:", string(body))

	// Auto Exit Checking
	if resp.StatusCode == 403 || resp.StatusCode == 401 || resp.StatusCode == 404 {
		// User Error somwhere!
		fmt.Println("The API has Declined to Accept the Submited EMail. " + string(resp.StatusCode))
		os.Exit(1)
	} else if resp.StatusCode == 500 {
		// User Error somwhere!
		fmt.Println("Issue with the Connecting Server. Got a 5xx response code. " + string(resp.StatusCode))
		os.Exit(1)
	} else {
		// Must have been a Good Response
		fmt.Println("Good Response, ", resp.StatusCode)
		fmt.Println("==")
		fmt.Println(body)
		fmt.Println("==")
	} // END: Sending EMail to Server Script
}
