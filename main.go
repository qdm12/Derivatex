package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/atotto/clipboard"
	"github.com/fatih/color"
	"github.com/mdp/qrterminal"
	ps "github.com/nbutton23/zxcvbn-go"
	"golang.org/x/crypto/ssh/terminal"
	pb "gopkg.in/cheggaaa/pb.v1"
)

// TODO clipboard Linux, Unix (requires 'xclip' or 'xsel' command to be installed)

func displayUsageAndExit() { // TODO update
	fmt.Println(color.HiWhiteString("Usage:") +
		color.HiCyanString("\n./derivatex create") + color.HiWhiteString("\nCreate the master password digest needed to generate passwords") +
		color.HiCyanString("\n\n./derivatex generate websitename [-length=20] [-user=email@domain.com]") + color.HiWhiteString("\nGenerate a password for a particular website name. Optionally set the password length and/or an identification different than the default one (stored in secretDigest)."))
	os.Exit(1)
}

func main() {
	createCommand := flag.NewFlagSet("create", flag.ExitOnError)
	generateCommand := flag.NewFlagSet("generate", flag.ExitOnError)
	passwordLengthPtr := generateCommand.Int("length", defaultPasswordLength, "Length of the derived password")
	userPtr := generateCommand.String("user", "", "Email, username or phone number used with this password")
	roundPtr := generateCommand.Int("round", 1, "Make higher than 1 if the password has to be renewed for the website")
	noSymbolPtr := generateCommand.Bool("nosymbol", false, "Force the password to contain no symbol")
	noDigitPtr := generateCommand.Bool("nodigit", false, "Force the password to contain no digit")
	noUppercasePtr := generateCommand.Bool("nouppercase", false, "Force the password to contain no uppercase letter")
	noLowercasePtr := generateCommand.Bool("nolowercase", false, "Force the password to contain no lowercase letter")
	excludeCharactersPtr := generateCommand.String("exclude", "", "Characters to exclude from the final password")
	notePtr := generateCommand.String("note", "", "Extra personal note you want to add")
	dumpCommand := flag.NewFlagSet("dump", flag.ExitOnError)
	dumpTablePtr := dumpCommand.String("table", defaultTableToDump, "SQLite table name to dump to CSV file")
	searchCommand := flag.NewFlagSet("search", flag.ExitOnError)
	// TODO search flags
	deleteCommand := flag.NewFlagSet("delete", flag.ExitOnError)
	// TODO delete flags
	listCommand := flag.NewFlagSet("list", flag.ExitOnError)
	// TODO list flags
	if len(os.Args) < 2 {
		displayUsageAndExit()
	}
	err := initiateDatabaseIfNeeded()
	if err != nil {
		color.HiRed("Error initiating database file '" + databaseFilename + "' (" + err.Error() + ")")
		return
	}
	var website string
	switch command := os.Args[1]; command {
	case "create":
		createCommand.Parse(os.Args[2:])
		if createCommand.Parsed() {
			createInteractive()
		}
	case "generate": // TODO make better with config options
		if len(os.Args) < 3 {
			color.HiRed("Website name is missing after command generate")
			displayUsageAndExit()
		}
		// TODO fix ./derivatex generate -length 5 facebook
		website = os.Args[2]
		generateCommand.Parse(os.Args[3:])
		if generateCommand.Parsed() {
			unallowedCharacters := buildUnallowedCharacters(*noSymbolPtr, *noDigitPtr, *noUppercasePtr, *noLowercasePtr, *excludeCharactersPtr)
			generate(website, *userPtr, uint8(*passwordLengthPtr), uint16(*roundPtr), unallowedCharacters, *notePtr)
		}
	case "dump":
		dumpCommand.Parse(os.Args[2:])
		if dumpCommand.Parsed() {
			err := dumpTable(*dumpTablePtr)
			if err != nil {
				color.HiRed("The table " + *dumpTablePtr + " could not be dumped to CSV file because: " + err.Error())
				return
			}
			color.HiGreen("The table " + *dumpTablePtr + " is saved in " + *dumpTablePtr + ".csv")
		}
	case "search":
		if len(os.Args) < 3 {
			color.HiRed("Search query string is missing after command search")
			displayUsageAndExit()
		}
		searchCommand.Parse(os.Args[3:])
		if searchCommand.Parsed() {
			identifications, err := searchIdentifications(os.Args[2])
			if err != nil {
				color.HiRed("The following error occurred when searching the identifications for '" + os.Args[2] + "': " + err.Error())
				return
			}
			if len(identifications) == 0 {
				color.HiWhite("No identifications found for query string '" + os.Args[2] + "'")
				return
			}
			color.HiWhite("Website | User | Unix time | Round | Program version")
			for i := range identifications {
				color.White(strings.Join(identifications[i].toStrings(), " | "))
			}
		}
	case "delete":
		if len(os.Args) < 3 {
			color.HiRed("Website string to delete is missing after command delete")
			displayUsageAndExit()
		}
		deleteCommand.Parse(os.Args[3:])
		if deleteCommand.Parsed() {
			website := os.Args[2]
			identifications, err := findIdentificationsByWebsite(website)
			if err != nil {
				color.HiRed("Error reading the database file '" + databaseFilename + "' (" + err.Error() + ")")
				return
			}
			if len(identifications) == 0 {
				color.Yellow("No identifications found for website '" + website + "'")
			} else if len(identifications) == 1 {
				err = deleteIdentification(website, identifications[0].user)
				if err != nil {
					color.HiRed("Error deleting the identification: " + err.Error())
					return
				}
				color.HiGreen("The following identification has been deleted from the database:\n" +
					strings.Join(identifications[0].toStrings(), " | "))
			} else {
				color.HiWhite(strings.Join(identificationTypeLegendStrings(), " | "))
				for i := range identifications {
					color.White(strings.Join(identifications[i].toStrings(), " | "))
				}
				var user string
				for {
					user = readInput("Please specify which user you want to delete: ")
					identification, err := findIdentification(website, user)
					if err != nil {
						color.HiRed("Error reading the database file '" + databaseFilename + "' (" + err.Error() + ")")
						return
					}
					if identification.website == "" { // not found
						color.Yellow("identification with website '" + website + "' and user '" + user + "' was not found. Please try again.")
						continue
					}
					break
				}
				err = deleteIdentification(website, user)
				if err != nil {
					color.HiRed("Error deleting the identification: " + err.Error())
					return
				}
				color.HiGreen("The identification has been deleted from the database")
			}
		}
	case "list":
		listCommand.Parse(os.Args[2:])
		if listCommand.Parsed() {
			identifications, err := getAllIdentifications()
			if err != nil {
				color.HiRed("Error reading the database file '" + databaseFilename + "' (" + err.Error() + ")")
				return
			}
			color.HiWhite(strings.Join(identificationTypeLegendStrings(), " | "))
			for _, identification := range identifications {
				color.White(strings.Join(identification.toStrings(), " | "))
			}
			color.HiGreen("Retrieved " + strconv.FormatInt(int64(len(identifications)), 10) + " identifications from database.")
		}
	default:
		color.HiRed("Command '" + command + "' not recognized.")
		displayUsageAndExit()
	}
}

func readInput(prompt string) (input string) {
	fmt.Print(color.HiMagentaString(prompt))
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		input = scanner.Text()
	}
	return input
}

func readSecret(prompt string) (secretPtr *[]byte, err error) {
	fmt.Print(color.HiMagentaString(prompt))
	secretPtr = new([]byte)
	*secretPtr, err = terminal.ReadPassword(int(syscall.Stdin))
	fmt.Print("\n")
	if err != nil {
		return nil, err
	}
	return secretPtr, nil
}

func generate(website, user string, passwordLength uint8, round uint16, unallowedCharacters unallowedCharactersType, note string) {
	if !unallowedCharacters.isAnythingAllowed() {
		color.HiRed("The password can't be generated with all possible characters excluded")
		return
	}
	defaultUser, protection, masterDigest, err := readMasterDigest()
	if err != nil {
		color.Yellow("An error occurred reading the master digest file: " + err.Error())
		return
	}
	if user == "" { // default flag
		user = defaultUser
	}
	newIdentification := identificationType{
		website:             website,
		user:                user,
		passwordLength:      passwordLength,
		round:               round,
		unallowedCharacters: unallowedCharacters.serialize(),
		creationTime:        time.Now().Unix(), // set to previous database record if a record is found
		programVersion:      version,
		note:                note,
	}
	identifications, err := findIdentificationsByWebsite(website)
	if err != nil {
		color.HiRed("Error reading the database file '" + databaseFilename + "' (" + err.Error() + ")")
		return
	}
	var replaceidentification bool
	if len(identifications) > 0 {
		var identificationExists bool
		for i := range identifications {
			if identifications[i].user == newIdentification.user { // identification already exists in database
				identificationExists = true
				if !identifications[i].generationParamsEqualTo(&newIdentification) {
					color.HiWhite("A password for the following identification has already been generated previously:")
					color.White(strings.Join(identificationTypeLegendStrings(), " | "))
					color.White(strings.Join(identifications[i].toStrings(), " | "))
					color.HiWhite("You are trying to create a password with the following identification:")
					color.White(strings.Join(identificationTypeLegendStrings(), " | "))
					color.White(strings.Join(newIdentification.toStrings(), " | "))
					continueGenerate := readInput("Replace the old identification? (yes/no) [no]: ")
					if continueGenerate != "yes" {
						return
					}
					replaceidentification = true
				} else { // same identification as stored in database
					newIdentification.creationTime = identifications[i].creationTime
				}
				break
			}
		}
		if !identificationExists {
			color.HiWhite("Password(s) for the following identification(s) have already been generated previously:")
			color.White(strings.Join(identificationTypeLegendStrings(), " | "))
			for i := range identifications {
				color.White(strings.Join(identifications[i].toStrings(), " | "))
			}
			continueGenerate := readInput("Generate a password for '" + website + "' and new user '" + user + "'? (yes/no) [no]: ")
			if continueGenerate != "yes" {
				return
			}
		}
	}
	if protection == "pin" {
		var pinCodeSHA3 *[32]byte
		var decryptedMasterDigest *[]byte
		for {
			pinCode, err := readSecret("Enter your PIN code to decrypt the master digest: ")
			if err != nil {
				color.Yellow("An error occurred reading the PIN code: " + err.Error())
				continue
			}
			valid, _ := regexp.Match("^[0-9]{4}$", *pinCode)
			if !valid {
				color.Yellow("The PIN code you entered is not in the valid format.")
				clearByteSlice(pinCode)
				continue
			}
			pinCodeSHA3 = hashAndDestroy(pinCode)
			decryptedMasterDigest, err = decryptAES(masterDigest, pinCodeSHA3)
			clearByteArray32(pinCodeSHA3)
			if err != nil {
				color.HiRed("Decryption error: " + err.Error())
				clearByteSlice(decryptedMasterDigest)
				continue
			}
			err = dechecksumize(decryptedMasterDigest)
			if err != nil {
				color.HiRed("Master digest or PIN Code is invalid - " + err.Error())
				clearByteSlice(decryptedMasterDigest)
				continue
			}
			clearByteSlice(masterDigest)
			masterDigest = decryptedMasterDigest
			break
		}
	}
	password := determinePassword(masterDigest, []byte(website), passwordLength, round, unallowedCharacters)
	// TODO transaction
	if replaceidentification {
		err := deleteIdentification(newIdentification.website, newIdentification.user)
		if err != nil {
			color.HiRed("Error deleting the identification: " + err.Error())
			return
		}
	}
	insertIdentification(newIdentification)
	color.HiGreen("Password QR Code:")
	config := qrterminal.Config{
		Level:     qrterminal.M,
		Writer:    os.Stdout,
		BlackChar: qrterminal.WHITE,
		WhiteChar: qrterminal.BLACK,
		QuietZone: 1,
	}
	qrterminal.GenerateWithConfig(password, config)
	fmt.Println(color.HiGreenString("Password: ") + color.HiWhiteString(password))
	clipboard.WriteAll(password)
	color.HiGreen("Password copied to clipboard")
}

func createInteractive() {
	fmt.Printf(color.HiWhiteString("Detecting performance of machine for Argon2ID..."))
	argonTimePerRound := getArgonTimePerRound()                       // depends on the machine
	fmt.Println(color.HiGreenString("%dms/round", argonTimePerRound)) // TODO in goroutine
	var masterPasswordSHA3, birthdateSHA3, pinCodeSHA3 *[32]byte
	for {
		for {
			masterPassword, err := readSecret("Enter your master password: ")
			if err != nil {
				color.Yellow("An error occurred reading the password: " + err.Error())
				continue
			}
			safety, message := evaluatePassword(masterPassword)
			masterPasswordSHA3 = hashAndDestroy(masterPassword)
			color.HiWhite(message)
			if safety == 0 {
				color.Yellow("Your password is not safe, please enter a more complicated password.")
				continue
			} else if safety == 1 {
				setStrongerPassword := readInput("Your password is not very safe, would you like to enter a stronger password? (yes/no) [no]: ")
				if setStrongerPassword == "yes" {
					clearByteArray32(masterPasswordSHA3)
					continue
				}
			} else {
				color.HiGreen("Your password is very safe, good job!")
			}
			masterPasswordConfirm, err := readSecret("Enter your master password again: ")
			if err != nil {
				color.Yellow("An error occurred reading the password confirmation: " + err.Error())
				continue
			}
			masterPasswordSHA3Confirm := hashAndDestroy(masterPasswordConfirm)
			if !byteArrays32Equal(masterPasswordSHA3, masterPasswordSHA3Confirm) {
				color.Yellow("The passwords entered do not match, please try again.")
				clearByteArray32(masterPasswordSHA3)
				clearByteArray32(masterPasswordSHA3Confirm)
				continue
			}
			clearByteArray32(masterPasswordSHA3Confirm)
			break
		}
		for {
			birthdate, err := readSecret("Enter your date of birth in the format dd/mm/yyyy: ")
			if err != nil {
				color.Yellow("An error occurred reading your birthdate: " + err.Error())
				continue
			}
			if !dateIsValid(birthdate) {
				color.Yellow("The birthdate you entered is not valid.")
				clearByteSlice(birthdate)
				continue
			}
			birthdateSHA3 = hashAndDestroy(birthdate)
			birthdateConfirm, err := readSecret("Enter your date of birth in the format dd/mm/yyyy again: ")
			if err != nil {
				color.Yellow("An error occurred reading your birthdate confirmation: " + err.Error())
				clearByteArray32(birthdateSHA3)
				continue
			}
			birthdateSHA3Confirm := hashAndDestroy(birthdateConfirm)
			if !byteArrays32Equal(birthdateSHA3, birthdateSHA3Confirm) {
				color.Yellow("The birthdates entered do not match, please try again.")
				clearByteArray32(birthdateSHA3)
				clearByteArray32(birthdateSHA3Confirm)
				continue
			}
			color.HiGreen("Your birthdate is valid.")
			break
		}
		var user string
		for {
			user = readInput("Enter your default user (i.e. email@domain.com): ")
			if user == "" {
				color.Yellow("Please enter a non-empty user")
				continue
			}
			break
		}
		color.HiWhite("Computing master digest...")
		stopchan := make(chan struct{})
		stoppedchan := make(chan struct{})
		go func() {
			defer close(stoppedchan)
			bar := pb.StartNew(int(argonTimeCost))
			bar.SetRefreshRate(time.Millisecond * 150)
			bar.ShowCounters = false
			var i uint32
			for {
				select {
				default:
					if i == argonTimeCost {
						bar.FinishPrint(color.HiGreenString("About to finish..."))
						return
					}
					bar.Increment()
					i++
					time.Sleep(time.Millisecond * time.Duration(argonTimePerRound))
				case <-stopchan:
					bar.FinishPrint(color.HiGreenString("Computation finished!"))
					return
				}
			}
		}()
		masterDigest := createMasterDigest(masterPasswordSHA3, birthdateSHA3)
		// masterDigest is argonDigestSize bytes long
		clearByteArray32(masterPasswordSHA3)
		clearByteArray32(birthdateSHA3)
		close(stopchan) // stop the progress bar
		<-stoppedchan   // wait for it to stop
		color.HiGreen("Master digest computed successfully")
		protection := "none"
		optionPin := readInput("[OPTIONAL] To generate a password, would you like to setup a 4 digit pin code? (yes/no) [no]: ")
		if optionPin == "yes" {
			protection = "pin"
			for {
				pinCode, err := readSecret("Please choose your PIN code in the format 9999: ")
				if err != nil {
					color.Yellow("An error occurred reading the PIN code: " + err.Error())
					continue
				}
				valid, _ := regexp.Match("^[0-9]{4}$", *pinCode)
				if !valid {
					color.Yellow("The PIN code you entered is not valid.")
					clearByteSlice(pinCode)
					continue
				}
				pinCodeSHA3 = hashAndDestroy(pinCode)
				pinCodeConfirm, err := readSecret("Please confirm your PIN code in the format 9999: ")
				if err != nil {
					color.Yellow("An error occurred reading the PIN code confirmation: " + err.Error())
					continue
				}
				pinCodeSHA3Confirm := hashAndDestroy(pinCodeConfirm)
				if !byteArrays32Equal(pinCodeSHA3, pinCodeSHA3Confirm) {
					color.Yellow("The PIN codes entered do not match, please try again.")
					clearByteArray32(pinCodeSHA3)
					clearByteArray32(pinCodeSHA3Confirm)
					continue
				}
				checksumize(masterDigest)
				masterDigest, err = encryptAES(masterDigest, pinCodeSHA3)
				if err != nil {
					color.HiRed("The following error occurred when encrypting the master digest: " + err.Error())
					continue
				}
				clearByteArray32(pinCodeSHA3)
				color.HiGreen("\nMaster digest encrypted using PIN code successfully")
				break
			}
		}
		// TODO Yubikey with https://github.com/tstranex/u2f
		err := writeMasterDigest(user, protection, masterDigest)
		clearByteSlice(masterDigest)
		if err != nil {
			color.HiRed("Error writing master digest to file: " + err.Error())
			continue
		}
		color.HiGreen("Master digest saved successfully!")
		break
	}
}

func displayTime(seconds float64) string {
	formater := "%.1f %s"
	minute := float64(60)
	hour := minute * float64(60)
	day := hour * float64(24)
	month := day * float64(31)
	year := month * float64(12)
	century := year * float64(100)

	if seconds < minute {
		return "a few seconds"
	} else if seconds < hour {
		return fmt.Sprintf(formater, (1 + math.Ceil(seconds/minute)), "minutes")
	} else if seconds < day {
		return fmt.Sprintf(formater, (1 + math.Ceil(seconds/hour)), "hours")
	} else if seconds < month {
		return fmt.Sprintf(formater, (1 + math.Ceil(seconds/day)), "days")
	} else if seconds < year {
		return fmt.Sprintf(formater, (1 + math.Ceil(seconds/month)), "months")
	} else if seconds < century {
		return fmt.Sprintf(formater, (1 + math.Ceil(seconds/century)), "years")
	} else {
		return "centuries"
	}
}

func evaluatePassword(masterPassword *[]byte) (safety uint8, message string) {
	analysis := ps.PasswordStrength(string(*masterPassword), []string{})
	// TODO find cracktime
	message = "Your password has an entropy of " + strconv.FormatFloat(analysis.Entropy, 'f', 2, 64) + " bits, equivalent to a suitcase lock of " + strconv.FormatFloat(analysis.Entropy*0.30103, 'f', 0, 64) + " digits."
	if analysis.Entropy > 30 {
		safety = 1
	}
	if analysis.Entropy > 50 {
		safety = 2
	}
	return safety, message
}

func bytes32ToUint32(b *[32]byte) (n *uint32) {
	n = new(uint32)
	*n = binary.LittleEndian.Uint32((*b)[:])
	return n
}
