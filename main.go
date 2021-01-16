
package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
)

func main() {

	//set up telegram info
	bot, err := tgbotapi.NewBotAPI(os.Getenv("TELEGRAM_TOKEN"))
	errCheck(err, "Failed to start telegram bot")
	log.Printf("Authorized on account %s", bot.Self.UserName)
	chatID, err := strconv.ParseInt(os.Getenv("CHAT_ID"), 10, 64)
	errCheck(err, "Failed to fetch chat IDs")

	client := &http.Client{}

	//for heroku
	go func() {
		http.ListenAndServe(":"+os.Getenv("PORT"),
			http.HandlerFunc(http.NotFound))
	}()
	for {
		//fetching cookies
		log.Println("Fetching cookies")
		sessionID := fetchCookies()
		
		log.Println("Logging in")
		err = logIn(os.Getenv("NRIC"), os.Getenv("PASSWORD"), sessionID, client)
		errCheck(err, "Error logging in")

		log.Println("Fetching slots")
		rawSlots, err := slotPage(os.Getenv("ACCOUNT_ID"), sessionID, client)
		errCheck(err, "Error getting slot page")

		log.Println("Parsing slots")
		slots, err := extractSlots(rawSlots)
		errCheck(err, "Error parsing slot page")

		log.Println("Extracting valid slots")
		valids := validSlots(slots)
		for _, validSlot := range valids { //for all the slots which meet the rule (i.e. within 10 days of now)
			log.Println("SlotID: " + validSlot.SlotID)
			book(os.Getenv("ACCOUNT_ID"), validSlot, sessionID, client)
			alert("Slot available (and booked) on " + validSlot.Date.Format("2 Jan 2006 (Mon)") + " " + os.Getenv("SESSION_"+validSlot.SessionNumber), bot, chatID)
		}
		log.Println("Finished getting slots")

		//Sleep for a random duration
		r := rand.Intn(300) + 120
		s := fmt.Sprint(time.Duration(r) * time.Second)
		alert("Retrigger in: "+s, bot, chatID)
		//time.AfterFunc(30*time.Second, ping)
		time.Sleep(time.Duration(r) * time.Second)
	}

}

// returns slots that should be autobooked/alerted about
func validSlots(slots []DrivingSlot) []DrivingSlot {
	valids := make([]DrivingSlot, 0)
	daysFromNow := int(slot.Date.Sub(time.Now()).Hours()/24) + 1
	daysToLookAhead, err := strconv.Atoi(os.Getenv("DAYSTOLOOKAHEAD"))
	for _, slot := range slots {
		if daysFromNow < daysToLookAhead { // if slot is within DAYSTOLOOKAHEAD days of now
			if slot.Date.Sub(time.Now()) > 1*(12*time.Hour) { // if slot is more than 12 hours from now
				valids = append(valids, slot)
			}

		}
	}
	return valids
}

func book(accountID string, slot DrivingSlot, sessionID *http.Cookie, client *http.Client) error {
	req, err := http.NewRequest("POST", "http://www.bbdc.sg/bbdc/b-2-pLessonBookingDetails.asp",
		strings.NewReader(paymentForm(accountID, slot.SlotID).Encode()))
	if err != nil {
		return errors.New("Error creating request: " + err.Error())
	}
	req.AddCookie(sessionID)
	req.AddCookie(&http.Cookie{Name: "language", Value: "en-US"})
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_, err = client.Do(req)
	if err != nil {
		return errors.New("Error sending request: " + err.Error())
	}

	return nil
}

func fetchCookies() (*http.Cookie) {
	resp, err := http.Get("http://www.bbdc.sg/bbdc/bbdc_web/newheader.asp")
	errCheck(err, "Error fetching cookies (sessionID)")
	sessionID := resp.Cookies()[0]
	return sessionID
}

// DrivingSlot represents a CDC slot to go for driving lessons
type DrivingSlot struct {
	SlotID        string
	Date          time.Time
	SessionNumber string
}

func logIn(nric string, pwd string, sessionID *http.Cookie, client *http.Client) error {
	loginForm := url.Values{}
	loginForm.Add("txtNRIC", nric)
	loginForm.Add("txtpassword", pwd)
	loginForm.Add("btnLogin", "ACCESS+TO+BOOKING+SYSTEM")
	req, err := http.NewRequest("POST", "http://www.bbdc.sg/bbdc/bbdc_web/header2.asp", strings.NewReader(loginForm.Encode()))
	if err != nil {
		return errors.New("Error creating request: " + err.Error())
	}
	req.AddCookie(sessionID)
	req.AddCookie(&http.Cookie{Name: "language", Value: "en-US"})
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	_, err = client.Do(req)
	if err != nil { // not checking for incorrect password, for fully secure version do check that in the response
		return errors.New("Error sending request: " + err.Error())
	}
	return nil
}

func extractSlots(slotPage string) ([]DrivingSlot, error) {
	// parse booking page to get booking dates
	// The data is hidden away in the following function call in the HTML page
	// fetched:
	// doTooltipV(event,0, "03/05/2019 (Fri)","3","11:30","13:10","BBDC");

	slotSections := strings.Split(slotPage, "doTooltipV(")[1:]
	slots := make([]DrivingSlot, 0)
	for _, slotSection := range slotSections {
		bookingData := strings.Split(slotSection, ",")[0:6]
		sessionNum := strings.Split(bookingData[3], "\"")[1] // strip of quotes
		rawDay := bookingData[2]                             // looks like  "03/05/2019 (Fri)"
		layout := "02/01/2006"
		day, err := time.Parse(layout, strings.Split(strings.Split(rawDay, "\"")[1], " ")[0]) // strip of quotes and remove the `(Fri)`
		if err != nil {
			return nil, errors.New("Error parsing date: " + err.Error())
		}

		//need to get slot ID for auto-book
		//strings.Split(substr, ",") returns- "BBDC"); SetMouseOverToggleColor("cell145_2") ' onmouseout='hideTip(); SetMouseOverToggleColor("cell145_2")'><input type="checkbox" id="145_2" name="slot" value="1893904" onclick="SetCountAndToggleColor('cell145_2'
		//splitting on value= and taking the second element returns- "1893904" onclick="SetCountAndToggleColor('cell145_2'
		//then split on " and take the second element to get 1893904
		slotID := strings.Split(strings.Split(strings.Split(slotSection, ",")[6], "value=")[1], "\"")[1]
		slots = append(slots, DrivingSlot{SlotID: slotID, Date: day, SessionNumber: sessionNum})
	}

	return slots, nil
}

func slotPage(accountID string,  sessionID *http.Cookie, client *http.Client) (string, error) {
	req, err := http.NewRequest("POST", "http://www.bbdc.sg/bbdc/b-2-pLessonBooking1.asp",
		strings.NewReader(bookingForm(accountID).Encode()))
	if err != nil {
		return "", errors.New("Error creating request: " + err.Error())
	}
	req.AddCookie(sessionID)
	req.AddCookie(&http.Cookie{Name: "language", Value: "en-US"})
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("Error sending request: " + err.Error())
	}
	body, _ := ioutil.ReadAll(resp.Body)
	// ioutil.WriteFile("booking.txt", body, 0644)
	return string(body), nil
}

func alert(msg string, bot *tgbotapi.BotAPI, chatID int64) {
	telegramMsg := tgbotapi.NewMessage(chatID, msg)
	bot.Send(telegramMsg)
	log.Println("Sent message to " + strconv.FormatInt(chatID, 10) + ": " + msg)
}

func paymentForm(accountID string, slotID string) url.Values {
	form := url.Values{}
	form.Add("accId", accountID)
	form.Add("slot", slotID)

	return form
}

func bookingForm(accountID string) url.Values {
	bookingForm := url.Values{}
	bookingForm.Add("accId", os.Getenv("ACCOUNT_ID"))
	months := strings.Split(os.Getenv("WANTED_MONTHS"), ",")

	sessions := strings.Split(os.Getenv("WANTED_SESSIONS"), ",")
	days := strings.Split(os.Getenv("WANTED_DAYS"), ",")
	for _, month := range months {
		bookingForm.Add("Month", month)
	}
	for _, session := range sessions {
		bookingForm.Add("Session", session)
	}
	for _, day := range days {
		bookingForm.Add("Day", day)
	}
	bookingForm.Add("defPLVenue", "1")
	bookingForm.Add("optVenue", "1")

	log.Printf("Looking through booking form for %s, for %s sessions, for these days %s (where 7 = Saturday etc.)", strings.Join(months, " "), strings.Join(sessions, " "), strings.Join(days, " "))

	return bookingForm
}

func errCheck(err error, msg string) {
	if err != nil {
		log.Fatal(msg + ": " + err.Error())
	}
}
