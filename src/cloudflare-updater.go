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

	CloudflareZone    string
	CloudflareRecords []string
	CloudflareToken   string
	CloudflareDnsTTL  int
	IpUrl             string
	IpFile            string
	IntervalMins      int
)

func init() {
	InfoLogger = log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	ErrorLogger = log.New(os.Stdout, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)

	if os.Getenv("LOG_LEVEL") == "debug" {
		DebugLogger = log.New(os.Stdout, "DEBUG: ", log.Ldate|log.Ltime|log.Lshortfile)
	} else {
		DebugLogger = log.New(io.Discard, "DEBUG: ", log.Ldate|log.Ltime|log.Lshortfile)
	}

	InfoLogger.Println("=========================")
	InfoLogger.Println(" Cloudflare DDNS Updater ")
	InfoLogger.Println("=========================")

	var missingEnvs []string
	var err error

	// Validate environment variables
	if os.Getenv("CLOUDFLARE_ZONE") == "" {
		missingEnvs = append(missingEnvs, "CLOUDFLARE_ZONE")
	} else {
		CloudflareZone = strings.TrimSpace(os.Getenv("CLOUDFLARE_ZONE"))
	}

	if os.Getenv("CLOUDFLARE_RECORDS") == "" {
		if os.Getenv("CLOUDFLARE_RECORD") == "" {
			missingEnvs = append(missingEnvs, "CLOUDFLARE_RECORD")
			missingEnvs = append(missingEnvs, "CLOUDFLARE_RECORDS")
		} else {
			CloudflareRecords = []string{strings.TrimSpace(os.Getenv("CLOUDFLARE_RECORD"))}
		}
	} else {
		CloudflareRecords = strings.Split(strings.TrimSpace(os.Getenv("CLOUDFLARE_RECORDS")), ",")
		// Trim whitespace from each record
		for i := range CloudflareRecords {
			CloudflareRecords[i] = strings.TrimSpace(CloudflareRecords[i])
		}
	}

	if os.Getenv("CLOUDFLARE_TOKEN") == "" {
		missingEnvs = append(missingEnvs, "CLOUDFLARE_TOKEN")
	} else {
		CloudflareToken = strings.TrimSpace(os.Getenv("CLOUDFLARE_TOKEN"))
	}

	if len(missingEnvs) > 0 {
		ErrorLogger.Fatalf("Missing required ENVs: %s", strings.Join(missingEnvs, ","))
	}

	if os.Getenv("IP_URL") == "" {
		IpUrl = "https://checkip.amazonaws.com"
	} else {
		IpUrl = strings.TrimSpace(os.Getenv("IP_URL"))
	}

	if os.Getenv("IP_FILE") == "" {
		IpFile = "/tmp/ip"
	} else {
		IpFile = strings.TrimSpace(os.Getenv("IP_FILE"))
	}

	if os.Getenv("INTERVAL_MINS") == "" {
		IntervalMins = 5
	} else {
		IntervalMins, err = strconv.Atoi(os.Getenv("INTERVAL_MINS"))
		if err != nil {
			ErrorLogger.Printf("Invalid interval '%s'. Defaulting to '5'", os.Getenv("INTERVAL_MINS"))
			IntervalMins = 5
		}
	}

	if os.Getenv("CLOUDFLARE_DNS_TTL") == "" {
		CloudflareDnsTTL = 1
	} else {
		CloudflareDnsTTL, err = strconv.Atoi(os.Getenv("CLOUDFLARE_DNS_TTL"))
		if err != nil {
			ErrorLogger.Printf("Invalid TTL '%s'. Defaulting to '1'", os.Getenv("CLOUDFLARE_DNS_TTL"))
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

func getIp() (string, error) {
	DebugLogger.Printf("Getting IP from '%s'", IpUrl)
	resp, err := http.Get(IpUrl)
	if err != nil {
		return "", fmt.Errorf("failed to get IP: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read IP response: %w", err)
	}

	ip := strings.TrimSpace(string(body))
	DebugLogger.Printf("Acquired IP: %s", ip)
	return ip, nil
}

func isIpChanged(currentIp string) bool {
	existingIp := ""
	if _, err := os.Stat(IpFile); err == nil {
		DebugLogger.Printf("Reading existing IP from '%s'", IpFile)
		data, err := os.ReadFile(IpFile)
		if err != nil {
			ErrorLogger.Printf("Failed to read IP file: %v", err)
			return true // Assume changed if we can't read
		}
		existingIp = strings.TrimSpace(string(data))
	} else {
		DebugLogger.Printf("No existing IP file found at '%s'", IpFile)
	}

	if existingIp != currentIp {
		InfoLogger.Printf("Updating IP (%s -> %s)", existingIp, currentIp)
		if err := os.WriteFile(IpFile, []byte(currentIp), 0644); err != nil {
			ErrorLogger.Printf("Failed to write IP file: %v", err)
		}
		return true
	}

	DebugLogger.Printf("IP (%s) hasn't changed. No update required.", currentIp)
	return false
}

func addAuthHeader(req *http.Request) {
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", CloudflareToken))
}

func doCloudflareRequest(client *http.Client, req *http.Request) (*http.Response, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode == 403 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("unauthorized - check your Cloudflare token")
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("cloudflare API error (status %d): %s", resp.StatusCode, string(body))
	}

	return resp, nil
}

func getZoneId(client *http.Client) (string, error) {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones?name=%s&status=active", CloudflareZone)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create zone request: %w", err)
	}
	addAuthHeader(req)

	DebugLogger.Printf("Getting Zone ID for '%s'", CloudflareZone)
	resp, err := doCloudflareRequest(client, req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read zone response: %w", err)
	}

	var cfResp CFResponse
	if err := json.Unmarshal(body, &cfResp); err != nil {
		return "", fmt.Errorf("failed to parse zone response: %w", err)
	}

	if len(cfResp.Result) == 0 {
		return "", fmt.Errorf("zone '%s' not found", CloudflareZone)
	}

	zoneId := cfResp.Result[0].Id
	DebugLogger.Printf("Zone ID for '%s' is '%s'", CloudflareZone, zoneId)
	return zoneId, nil
}

func getRecordId(client *http.Client, zoneId, recordName string) (string, error) {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records?type=A&name=%s", zoneId, recordName)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create record request: %w", err)
	}
	addAuthHeader(req)

	DebugLogger.Printf("Getting Record ID for '%s'", recordName)
	resp, err := doCloudflareRequest(client, req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read record response: %w", err)
	}

	var cfResp CFResponse
	if err := json.Unmarshal(body, &cfResp); err != nil {
		return "", fmt.Errorf("failed to parse record response: %w", err)
	}

	if len(cfResp.Result) == 0 {
		DebugLogger.Printf("No Record ID found for '%s'", recordName)
		return "", nil // Empty string indicates record doesn't exist
	}

	recordId := cfResp.Result[0].Id
	DebugLogger.Printf("Record ID for '%s' is '%s'", recordName, recordId)
	return recordId, nil
}

func updateDnsRecord(client *http.Client, zoneId, recordId, recordName, ip string) error {
	cfReq := CFRequest{
		Type:    "A",
		Name:    recordName,
		Content: ip,
		TTL:     CloudflareDnsTTL,
		Proxied: false,
	}

	cfReqJson, err := json.Marshal(cfReq)
	if err != nil {
		return fmt.Errorf("failed to marshal DNS request: %w", err)
	}

	var url string
	var method string

	if recordId == "" {
		// Create new record
		DebugLogger.Printf("Creating new DNS record for '%s'", recordName)
		url = fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records", zoneId)
		method = "POST"
	} else {
		// Update existing record
		DebugLogger.Printf("Updating DNS record for '%s'", recordName)
		url = fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/%s", zoneId, recordId)
		method = "PUT"
	}

	req, err := http.NewRequest(method, url, bytes.NewBuffer(cfReqJson))
	if err != nil {
		return fmt.Errorf("failed to create DNS update request: %w", err)
	}
	addAuthHeader(req)
	req.Header.Add("Content-Type", "application/json")

	resp, err := doCloudflareRequest(client, req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	InfoLogger.Printf("DNS Updated (%s -> %s)", recordName, ip)
	return nil
}

func updateCloudflare(currentIp string) {
	client := &http.Client{}

	zoneId, err := getZoneId(client)
	if err != nil {
		ErrorLogger.Printf("Failed to get zone ID: %v", err)
		return
	}

	// Track success/failure
	successCount := 0
	failureCount := 0

	for _, cfRecord := range CloudflareRecords {
		recordId, err := getRecordId(client, zoneId, cfRecord)
		if err != nil {
			ErrorLogger.Printf("Failed to get record ID for '%s': %v", cfRecord, err)
			failureCount++
			continue
		}

		err = updateDnsRecord(client, zoneId, recordId, cfRecord, currentIp)
		if err != nil {
			ErrorLogger.Printf("Failed to update DNS record '%s': %v", cfRecord, err)
			failureCount++
			continue
		}

		successCount++
	}

	if failureCount > 0 {
		ErrorLogger.Printf("DNS update completed with errors: %d succeeded, %d failed", successCount, failureCount)
	} else {
		InfoLogger.Printf("All DNS records updated successfully (%d records)", successCount)
	}
}

func main() {
	InfoLogger.Printf("Running every %d minutes", IntervalMins)

	// Get initial IP
	currentIp, err := getIp()
	if err != nil {
		ErrorLogger.Fatalf("Failed to get initial IP: %v", err)
	}
	InfoLogger.Printf("Current IP: %s", currentIp)

	for {
		currentIp, err := getIp()
		if err != nil {
			ErrorLogger.Printf("Failed to get IP: %v", err)
			time.Sleep(time.Duration(IntervalMins) * time.Minute)
			continue
		}

		if isIpChanged(currentIp) {
			updateCloudflare(currentIp)
		}

		time.Sleep(time.Duration(IntervalMins) * time.Minute)
	}
}
