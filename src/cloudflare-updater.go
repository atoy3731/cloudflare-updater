package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	DebugLogger *log.Logger
	InfoLogger  *log.Logger
	ErrorLogger *log.Logger

	CloudflareZone   string
	CloudflareRecord string
	CloudflareToken  string
	CloudflareDnsTTL int
	IpUrl            string
	ExistingIp       []byte
	IntervalMins     int
)

func init() {
	InfoLogger = log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	ErrorLogger = log.New(os.Stdout, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)

	if os.Getenv("LOG_LEVEL") == "debug" {
		DebugLogger = log.New(os.Stdout, "DEBUG: ", log.Ldate|log.Ltime|log.Lshortfile)
	} else {
		file, _ := os.OpenFile("/dev/null", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
		DebugLogger = log.New(file, "DEBUG: ", log.Ldate|log.Ltime|log.Lshortfile)
	}

	InfoLogger.Println("=========================")
	InfoLogger.Println(" Cloudflare DDNS Updater ")
	InfoLogger.Println("=========================")

	var missingEnvs []string
	var err error

	ExistingIp = []byte("N/A")

	// Validate environment variables
	if os.Getenv("CLOUDFLARE_ZONE") == "" {
		missingEnvs = append(missingEnvs, "CLOUDFLARE_ZONE")
	} else {
		CloudflareZone = strings.TrimSpace(string(os.Getenv("CLOUDFLARE_ZONE")))
	}

	if os.Getenv("CLOUDFLARE_RECORD") == "" {
		missingEnvs = append(missingEnvs, "CLOUDFLARE_RECORD")
	} else {
		CloudflareRecord = strings.TrimSpace(string(os.Getenv("CLOUDFLARE_RECORD")))
	}

	if os.Getenv("CLOUDFLARE_TOKEN") == "" {
		missingEnvs = append(missingEnvs, "CLOUDFLARE_TOKEN")
	} else {
		CloudflareToken = strings.TrimSpace(string(os.Getenv("CLOUDFLARE_TOKEN")))
	}

	if len(missingEnvs) > 0 {
		ErrorLogger.Fatalln(fmt.Sprintf("Missing required ENVs: %s", strings.Join(missingEnvs, ",")))
	}

	if os.Getenv("IP_URL") == "" {
		IpUrl = "https://checkip.amazonaws.com"
	} else {
		IpUrl = strings.TrimSpace(string(os.Getenv("IP_URL")))
	}

	if os.Getenv("INTERVAL_MINS") == "" {
		IntervalMins = 5
	} else {
		IntervalMins, err = strconv.Atoi(os.Getenv("INTERVAL_MINS"))
		if err != nil {
			ErrorLogger.Println(fmt.Sprintf("Invalid interval '%s'. Defaulting to '5'", os.Getenv("INTERVAL_MINS")))
			IntervalMins = 5
		}
	}

	if os.Getenv("CLOUDFLARE_DNS_TTL") == "" {
		CloudflareDnsTTL = 1
	} else {
		CloudflareDnsTTL, err = strconv.Atoi(os.Getenv("CLOUDFLARE_DNS_TTL"))
		if err != nil {
			ErrorLogger.Println(fmt.Sprintf("Invalid TTL '%s'. Defaulting to '1'", os.Getenv("CLOUDFLARE_DNS_TTL")))
			CloudflareDnsTTL = 1
		}
	}

}

type CFRequest struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

type CFResult struct {
	Id string `json:"id"`
}

type CFResponse struct {
	Result []CFResult `json:"result"`
}

func check(e error) {
	if e != nil {
		panic(e)
	}
}

func getIp() []byte {
	DebugLogger.Println(fmt.Sprintf("Getting IP from '%s'", IpUrl))
	resp, err := http.Get(IpUrl)
	if err != nil {
		log.Fatalln(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	DebugLogger.Println(fmt.Sprintf("Acquired IP: %s", strings.TrimSpace(string(body))))
	return body
}

func isIpChanged(current_ip []byte) bool {
	if string(ExistingIp) != string(current_ip) {
		InfoLogger.Println(fmt.Sprintf("Updating IP (%s -> %s)", strings.TrimSpace(string(ExistingIp)), strings.TrimSpace(string(current_ip))))
		return true
	} else {
		DebugLogger.Println(fmt.Sprintf("IP (%s) hasn't changed. No update required.", strings.TrimSpace(string(current_ip))))
	}

	return false
}

func addAuthHeader(req *http.Request) {
	header := fmt.Sprintf("Bearer %s", CloudflareToken)
	req.Header.Add("Authorization", header)
}

func updateCloudflare(current_ip []byte) {
	client := &http.Client{}

	// Get Zone ID
	var url = fmt.Sprintf("https://api.cloudflare.com/client/v4/zones?name=%s&status=active", CloudflareZone)
	var req, _ = http.NewRequest("GET", url, nil)

	addAuthHeader(req)

	DebugLogger.Println(fmt.Sprintf("Getting Zone ID for '%s'", CloudflareZone))
	var resp, resp_err = client.Do(req)
	if resp_err != nil {
		log.Fatalln(resp_err)
		return
	} else if resp.StatusCode == 403 {
		log.Fatalln("[ERROR] Unauthorized. Check your Cloudflare token!")
		return
	} else if resp.StatusCode != 200 {
		log.Fatalln("[ERROR] Issue contacting Cloudflare")
		b, _ := io.ReadAll(resp.Body)
		ErrorLogger.Println(fmt.Sprintf("  -> Body: %s", string(b)))
		return
	}

	var cfResp = CFResponse{}

	defer resp.Body.Close()
	var body, _ = io.ReadAll(resp.Body)
	json.Unmarshal(body, &cfResp)
	zoneId := cfResp.Result[0].Id
	DebugLogger.Println(fmt.Sprintf("Zone ID for '%s' is '%s'", CloudflareZone, zoneId))

	// Get Record ID
	url = fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records?type=A&name=%s", string(zoneId), CloudflareRecord)
	req, _ = http.NewRequest("GET", url, nil)
	addAuthHeader(req)

	DebugLogger.Println(fmt.Sprintf("Getting Record ID for '%s'", CloudflareRecord))
	resp, resp_err = client.Do(req)
	if resp_err != nil {
		log.Fatalln(resp_err)
		return
	} else if resp.StatusCode == 403 {
		log.Fatalln("[ERROR] Unauthorized. Check your Cloudflare token!")
		return
	} else if resp.StatusCode != 200 {
		log.Fatalln("[ERROR] Issue contacting Cloudflare")
		b, _ := io.ReadAll(resp.Body)
		ErrorLogger.Println(fmt.Sprintf("  -> Body: %s", string(b)))
		return
	}
	cfResp = CFResponse{}

	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	json.Unmarshal(body, &cfResp)

	if len(cfResp.Result) == 0 {
		DebugLogger.Println(fmt.Sprintf("No Record ID Found for '%s'. Creating new record.", CloudflareRecord))
		var cfReq = CFRequest{}
		cfReq.Type = "A"
		cfReq.Name = CloudflareRecord
		cfReq.Content = strings.TrimSpace(string(current_ip))
		cfReq.TTL = CloudflareDnsTTL
		cfReq.Proxied = false

		cfReqJson, _ := json.Marshal(cfReq)
		url = fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records", string(zoneId))
		req, _ = http.NewRequest("POST", url, bytes.NewBuffer([]byte(cfReqJson)))
		addAuthHeader(req)
		req.Header.Add("Content-type", "application/json")

	} else {
		recordId := cfResp.Result[0].Id
		DebugLogger.Println(fmt.Sprintf("Record ID for '%s' is '%s'", CloudflareRecord, recordId))

		// Update record
		var cfReq = CFRequest{}
		cfReq.Type = "A"
		cfReq.Name = CloudflareRecord
		cfReq.Content = strings.TrimSpace(string(current_ip))
		cfReq.TTL = CloudflareDnsTTL
		cfReq.Proxied = false

		cfReqJson, _ := json.Marshal(cfReq)
		url = fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/%s", string(zoneId), string(recordId))
		req, _ = http.NewRequest("PUT", url, bytes.NewBuffer([]byte(cfReqJson)))
		addAuthHeader(req)
		req.Header.Add("Content-type", "application/json")
	}

	DebugLogger.Println(fmt.Sprintf("Creating/Updating DNS record for '%s'", CloudflareRecord))
	resp, resp_err = client.Do(req)
	if resp_err != nil {
		log.Fatalln(resp_err)
		return
	} else if resp.StatusCode == 403 {
		ErrorLogger.Fatalln("[ERROR] Unauthorized. Check your Cloudflare token!")
		return
	} else if resp.StatusCode != 200 {
		ErrorLogger.Fatalln("[ERROR] Issue contacting Cloudflare")
		b, _ := io.ReadAll(resp.Body)
		ErrorLogger.Println(fmt.Sprintf("  -> Body: %s", string(b)))
		return
	}

	InfoLogger.Println(fmt.Sprintf("DNS Updated (%s -> %s)", CloudflareRecord, strings.TrimSpace(string(string(current_ip)))))

}

func main() {
	InfoLogger.Println(fmt.Sprintf("Running every %d minutes", IntervalMins))
	InfoLogger.Println(fmt.Sprintf("Current IP: %s", strings.TrimSpace(string(getIp()))))

	for true {
		current_ip := getIp()

		if isIpChanged(current_ip) {
			updateCloudflare(current_ip)
		}

		time.Sleep(time.Duration(IntervalMins) * time.Minute)
	}
}
