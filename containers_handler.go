package main

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"math/rand"
	"net/url"
	"os"
	"strconv"
	"time"
)

//-------------------------------------Common Utils --------------------------------------------------------------------
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// getRequiredEnv gets an environment variable by name and returns an error if it is not found
func getRequiredEnv(name string) (string, error) {
	env, ok := os.LookupEnv(name)
	if ok {
		return env, nil
	} else {
		return "", errors.New("Required environment variable not set: " + name)
	}
}

const containerNameMaxSize = 15

func generateContainerName() string {
	var allowedChars = []rune("abcdefghijklmnopqrstuvwxyz")
	var seededRand = rand.New(rand.NewSource(time.Now().UnixNano()))
	n := max(3, seededRand.Intn(containerNameMaxSize)+1)
	b := make([]rune, n)
	for i := range b {
		b[i] = allowedChars[seededRand.Intn(len(allowedChars))]
	}
	return string(b)
}

//--------------------------------------Location: Blob -----------------------------------------------------------------

const AccountNameEnvVar = "AZURE_STORAGE_ACCOUNT_NAME"
const AccountKeyEnvVar = "AZURE_STORAGE_ACCOUNT_KEY"

type blobAccountType string

const (
	blobAccountDefault   blobAccountType = ""
	blobAccountSecondary blobAccountType = "SECONDARY_"
)

func getGenericCredential(accountType blobAccountType) (*azblob.SharedKeyCredential, error) {
	accountNameEnvVar := string(accountType) + AccountNameEnvVar
	accountName, err := getRequiredEnv(accountNameEnvVar)
	if err != nil {
		return nil, err
	}

	accountKeyEnvVar := string(accountType) + AccountKeyEnvVar
	accountKey, err := getRequiredEnv(accountKeyEnvVar)
	if err != nil {
		return nil, err
	}

	if accountName == "" || accountKey == "" {
		return nil, errors.New(string(accountType) + AccountNameEnvVar + " and/or " + string(accountType) +
			AccountKeyEnvVar + " environment variables not specified.")
	}
	return azblob.NewSharedKeyCredential(accountName, accountKey)
}

func getServiceClient(accountType blobAccountType, options *azblob.ClientOptions) (azblob.ServiceClient, error) {
	cred, err := getGenericCredential(accountType)
	if err != nil {
		return azblob.ServiceClient{}, err
	}

	serviceURL, _ := url.Parse("https://" + cred.AccountName() + ".blob.core.windows.net/")
	serviceClient, err := azblob.NewServiceClientWithSharedKey(serviceURL.String(), cred, options)

	return serviceClient, err
}

func createNewContainer(containerName string, serviceClient azblob.ServiceClient) (azblob.ContainerClient, error) {
	containerClient := serviceClient.NewContainerClient(containerName)
	cResp, err := containerClient.Create(context.Background(), nil)
	if err != nil {
		return azblob.ContainerClient{}, err
	} else if cResp.RawResponse.StatusCode != 201 {
		return azblob.ContainerClient{}, errors.New("Could not create container:" + cResp.RawResponse.Status)
	}
	return containerClient, err
}

func getContainerSAS(accountType blobAccountType, client azblob.ContainerClient, start time.Time, expiry time.Time) []string {
	credentials, err := getGenericCredential(accountType)
	urlParts := azblob.NewBlobURLParts(client.URL())
	sas, err := azblob.BlobSASSignatureValues{
		ContainerName: urlParts.ContainerName,
		Permissions:   azblob.ContainerSASPermissions{Read: true, Add: true, Create: true, Write: true, Delete: true, List: true}.String(),
		StartTime:     start.UTC(),
		ExpiryTime:    expiry.UTC(),
	}.NewSASQueryParameters(credentials)
	if err != nil {
		_, _ = client.Delete(context.Background(), nil)
		fmt.Println(err)
		os.Exit(1)
	}
	urlParts.SAS = sas
	return []string{urlParts.URL()}
}

func deleteContainer(accountType blobAccountType, containerName string) {
	svcClient, err := getServiceClient(accountType, nil)
	if err != nil {
		fmt.Printf("Failed to get serviceClient due to error: %s\n", err.Error())
	}

	_, err = svcClient.DeleteContainer(context.Background(), containerName, nil)
	if err != nil {
		fmt.Printf("Failed to delete the container %s due to error: %s\n", containerName, err.Error())
	} else {
		fmt.Printf("Successfully deleted container: %s\n", containerName)
	}
}

func WriteToFile(path string, data [][]string) {
	if len(data) == 0 {
		fmt.Println("Empty data!")
		os.Exit(1)
	}
	fileHandler, err := os.Create(path)
	if err != nil {
		fmt.Println(err.Error())
	}
	defer func(folders *os.File) {
		_ = folders.Close()
	}(fileHandler)

	writer := csv.NewWriter(fileHandler)
	defer writer.Flush()
	//_ = writer.Write([]string{"Locations"})

	for i := 0; i < len(data); i++ {
		_ = writer.Write([]string{data[i][0]})
	}
	writer.Flush()
}

func createLocationB(azcopyVersion string, localPath string, hours time.Duration) {
	svcClient, err := getServiceClient(blobAccountDefault, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	data := make([][]string, 0)
	data = append(data, []string{localPath})
	containerClient, err := createNewContainer(generateContainerName(), svcClient)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	data = append(data, getContainerSAS(blobAccountDefault, containerClient, time.Now(), time.Now().Add(hours*time.Hour)))
	WriteToFile("locationB"+azcopyVersion+".csv", data)
}

func createLocationC(azcopyVersion string, containerName string, hours time.Duration) {
	svcClient1, err := getServiceClient(blobAccountDefault, nil)
	if err != nil {
		fmt.Println(err)
		return
	}

	svcClient2, err := getServiceClient(blobAccountSecondary, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	data := make([][]string, 0)
	containerClient1 := svcClient1.NewContainerClient(containerName)
	data = append(data, getContainerSAS(blobAccountDefault, containerClient1, time.Now(), time.Now().Add(hours*time.Hour)))

	containerClient2, err := createNewContainer(generateContainerName(), svcClient2)
	data = append(data, getContainerSAS(blobAccountSecondary, containerClient2, time.Now(), time.Now().Add(hours*time.Hour)))
	WriteToFile("locationC"+azcopyVersion+".csv", data)
}

func createLocationD(azcopyVersion string, containerName string, hours time.Duration, localPath string) {
	svcClient, err := getServiceClient(blobAccountDefault, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	data := make([][]string, 0)
	containerClient1 := svcClient.NewContainerClient(containerName)
	data = append(data, getContainerSAS(blobAccountSecondary, containerClient1, time.Now(), time.Now().Add(hours*time.Hour)))
	data = append(data, []string{localPath})
	WriteToFile("locationD"+azcopyVersion+".csv", data)
}

func getContainerName(containerURL string) string {
	urlParts := azblob.NewBlobURLParts(containerURL)
	return urlParts.ContainerName
}

func publishResults(localPath, containerName string, hours time.Duration) {
	svcClient1, err := getServiceClient(blobAccountDefault, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	data := make([][]string, 0)
	data = append(data, []string{localPath + "/*.csv"})

	containerClient1 := svcClient1.NewContainerClient(containerName)
	data = append(data, getContainerSAS(blobAccountDefault, containerClient1, time.Now(), time.Now().Add(hours*time.Hour)))
	WriteToFile("publishResultsLocation.csv", data)
}

func main() {
	// A (Local) --- upload ---> B (Container1) ---- S2S ---> C (container2) --- Download ---> D (Local)
	// Create A by running local_file_generator.sh
	// run "sh local_file_generator.sh"
	arguments := os.Args[1:]
	switch arguments[0] {
	case "locB":
		localPath := arguments[1]
		sasValidityDuration, _ := strconv.Atoi(arguments[2])
		azcopyVersion := arguments[3]
		createLocationB(azcopyVersion, localPath, time.Duration(sasValidityDuration))
	case "locC":
		containerName := getContainerName(arguments[1])
		sasValidityDuration, _ := strconv.Atoi(arguments[2])
		azcopyVersion := arguments[3]
		createLocationC(azcopyVersion, containerName, time.Duration(sasValidityDuration))
	case "locD":
		containerName := getContainerName(arguments[1])
		sasValidityDuration, _ := strconv.Atoi(arguments[2])
		localPath := arguments[3]
		azcopyVersion := arguments[4]
		createLocationD(azcopyVersion, containerName, time.Duration(sasValidityDuration), localPath)
	case "delLocB":
		containerName := getContainerName(arguments[1])
		deleteContainer(blobAccountDefault, containerName)
	case "delLocC":
		containerName := getContainerName(arguments[1])
		deleteContainer(blobAccountSecondary, containerName)
	case "pubRes":
		localPath := arguments[1]
		containerName := arguments[2]
		sasValidityDuration, _ := strconv.Atoi(arguments[3])
		publishResults(localPath, containerName, time.Duration(sasValidityDuration))
	default:
		fmt.Println("Incorrect argument " + arguments[0])
	}
}
