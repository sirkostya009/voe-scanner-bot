package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	css "github.com/andybalholm/cascadia"
	tg "github.com/mymmrac/telego"
	"golang.org/x/net/html"
)

type VoeAddress struct {
	CityId, StreetId, HouseId, Street, House string
}

type VoeResult struct {
	Addr VoeAddress
	Res  string
}

type Time struct {
	StartTime int
	EndTime   int
	Confirmed bool
}

var (
	todayQuery    = css.MustCompile(".disconnection-detailed-table-container div:nth-child(n+27):nth-child(-n+50)")
	tomorrowQuery = css.MustCompile(".disconnection-detailed-table-container div:nth-child(n+52):nth-child(-n+75)")
)

func main() {
	chatId, err := strconv.ParseInt(os.Getenv("CHAT_ID"), 10, 64)
	if err != nil {
		println("invalid CHAT_ID", err.Error())
		return
	}
	if _, present := os.LookupEnv("TELEGRAM_BOT_TOKEN"); !present {
		println("TELEGRAM_BOT_TOKEN was not set")
		return
	}
	if _, present := os.LookupEnv("FORM_DATA"); !present {
		println("FORM_DATA was not set")
		return
	}

	var voeAddresses []VoeAddress
	for _, v := range strings.Split(os.Getenv("FORM_DATA"), ";") {
		vals := strings.Split(v, "-")
		if len(vals) != 5 {
			println("wrong form data", v, "must match CITY_ID-STREET_ID-HOUSE_ID-STREET_NAME-HOUSE_NUM")
			continue
		}

		voeAddresses = append(voeAddresses, VoeAddress{
			vals[0], vals[1], vals[2], vals[3], vals[4],
		})
	}

	bot, _ := tg.NewBot(os.Getenv("TELEGRAM_BOT_TOKEN"))

	requests := make(chan VoeResult)

	for _, addr := range voeAddresses {
		go request(requests, addr)
	}

	today := make(map[VoeAddress][]Time)
	tomorrow := make(map[VoeAddress][]Time)
	for range voeAddresses {
		res := <-requests
		if res.Res == "" {
			continue
		}
		doc, err := html.Parse(strings.NewReader(res.Res))

		if err != nil {
			println("html parse failed", err.Error())
			continue
		}

		today[res.Addr] = parseTimes(doc, todayQuery)
		tomorrow[res.Addr] = parseTimes(doc, tomorrowQuery)
	}

	for _, s := range [...]string{makeReport(today, "сьодні"), makeReport(tomorrow, "завтра")} {
		_, err = bot.SendMessage(&tg.SendMessageParams{
			ChatID:    tg.ChatID{ID: chatId},
			Text:      s,
			ParseMode: tg.ModeMarkdownV2,
		})
		if err != nil {
			println("failed to send message to telegram", err.Error())
		}
	}
}

func request(ch chan<- VoeResult, addr VoeAddress) {
	form := url.Values{}
	form["city_id"] = []string{addr.CityId}
	form["street_id"] = []string{addr.StreetId}
	form["house_id"] = []string{addr.HouseId}
	form["form_id"] = []string{"disconnection_detailed_search_form"}

	const formEndpoint = "https://www.voe.com.ua/disconnection/detailed?ajax_form=1&_wrapper_format=drupal_ajax&_wrapper_format=drupal_ajax"
	res, err := http.PostForm(formEndpoint, form)
	if err != nil {
		println("request failed", addr.Street, addr.House, err.Error())
		ch <- VoeResult{}
		return
	}

	var body []map[string]any
	err = json.NewDecoder(res.Body).Decode(&body)
	if err != nil {
		println("failed to decode response", err.Error())
		ch <- VoeResult{}
		return
	}

	ch <- VoeResult{addr, body[3]["data"].(string)}
}

func parseTimes(doc *html.Node, query css.Selector) (t []Time) {
	var (
		times         = make([]int, 0, 24)
		duration      = make([]int, 0, 24)
		confirmations = make([]bool, 0, 24)
	)
	for i, n := range css.QueryAll(doc, query) {
		for _, attr := range n.Attr {
			if attr.Key != "class" || !strings.Contains(attr.Val, "has_disconnection") {
				continue
			}

			pos := len(times) - 1
			if len(times) > 0 && times[pos]+duration[pos] == i {
				duration[pos]++
				break
			}
			times = append(times, i)
			duration = append(duration, 1)
			confirmations = append(confirmations, strings.Contains(attr.Val, "confirm_1"))
			break
		}
	}
	for i, tm := range times {
		t = append(t, Time{tm, tm + duration[i], confirmations[i]})
	}
	return
}

func makeReport(times map[VoeAddress][]Time, day string) string {
	buf := strings.Builder{}
	buf.Grow(300 * len(times))
	buf.WriteString(fmt.Sprintf("Вижимка на %s:\n\n", day))

	for _, addr := range slices.SortedFunc(maps.Keys(times), voeSort) {
		buf.WriteString(fmt.Sprintf("*%s, %s*:\n", addr.Street, addr.House))

		for _, time := range times[addr] {
			buf.WriteString(fmt.Sprintf("`%02d:00—%02d:00`", time.StartTime, time.EndTime))
			if time.Confirmed {
				buf.WriteString(" \\(підтверджено\\)")
			}

			buf.WriteRune('\n')
		}

		if len(times[addr]) == 0 {
			buf.WriteString("ВІДКЛЮЧЕНЬ НЕМАААА\\!\\!\\!\\!\\!\n")
		}

		buf.WriteRune('\n')
	}

	return buf.String()
}

func voeSort(a, b VoeAddress) int {
	return strings.Compare(a.Street, b.Street)
}
