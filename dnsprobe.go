package main

import (
  "bufio"
  "expvar"
  "fmt"
  "log"
  "net/http"
  "os"
  "path"
  "strconv"
  "strings"
  "time"
  "github.com/tonnerre/godns"
)

var MASTER_POLL_INTERVAL = 1 * time.Second
var slaves []string

type DnsServer struct {
  hostport, output_dir string
  file *os.File
  dns_client dns.Client
  dns_query *dns.Msg
  responses chan Response
}

type Response struct {
  hostport string
  txtrecord, queried_at int64
  query_ms float64
}

// query once and write the output to the provided file handle
func (s *DnsServer) query() float64 {
  t := time.Now()
  resp, rtt, err := s.dns_client.Exchange(s.dns_query, s.hostport)
  if err != nil {
    log.Printf("Error reading from %s: %s (%s)", s.hostport, err, rtt)
    // if we get a straight-up error (typically i/o timeout), skip it
    return 0
  }
  query_ms := float64(rtt) / 1000000

  // We received an empty respose
  if resp == nil || len(resp.Answer) == 0 {
    log.Printf("empty response from %s", s.hostport)
    s.responses <- Response{s.hostport, 0, t.Unix(), query_ms}
    return 0
  }

  // Parse the response into an int64
  slave_response := resp.Answer[0].(*dns.TXT).Txt[0]
  txtrecord, err := strconv.ParseInt(slave_response, 10, 0)
  if err != nil {
    log.Printf("Bad data from %s: %s", s.hostport, err)
    s.responses <- Response{s.hostport, 0, t.Unix(), query_ms}
    return query_ms
  }

  s.responses <- Response{s.hostport, txtrecord, t.Unix(), query_ms}
  return query_ms
}


// loop forever polling the slave
func (s *DnsServer) pollslave() {
  dns_client := &dns.Client{}
  dns_client.ReadTimeout = 3 * time.Second  // TODO: 30 seconds
  s.dns_client = *dns_client
  ticker := time.NewTicker(5 * time.Second) // TODO: 300 seconds
  for {
    s.query()
    <-ticker.C
  }
}


func (s *DnsServer) pollmaster() {
  var queries_sent_varz = expvar.NewInt("master-queries-sent")
  var queries_avg_varz = expvar.NewFloat("master-queries-avg-ms")
  var queries_sent int64
  var queries_avg, cumulative_latency float64
  dns_client := &dns.Client{}
  dns_client.ReadTimeout = 3 * time.Second  // TODO: 30 seconds
  s.dns_client = *dns_client
  ticker := time.NewTicker(MASTER_POLL_INTERVAL) // TODO: 300 seconds
  for {
    <-ticker.C  // Don't send a flood of queries in a restart loop...
    // Send the query to the master
    query_ms := s.query()

    // Notify the main thread that we've made a successful first poll
    queries_sent++
    queries_sent_varz.Set(queries_sent)
    cumulative_latency = cumulative_latency + query_ms
    queries_avg = cumulative_latency / float64(queries_sent)
    queries_avg_varz.Set(queries_avg)
  }
}

func compare_responses(output_dir string, master_responses,
                       slave_responses chan Response) {
  files := make(map[string]*os.File)
  filemode := os.O_CREATE|os.O_APPEND|os.O_WRONLY
  var master_value, master_queried_at int64
  var last_master_poll = expvar.NewInt("last-successful-master-poll")
  var master_skew = int64(MASTER_POLL_INTERVAL)/1000000000 * 5
  
  // TODO: populate the files map outside the select, cleaner

  for {
    select {
    case r := <-master_responses:
      if r.txtrecord == 0 {
        continue  // skip bad polls of the master
      }
      //log.Printf("Got master response: %d", r.txtrecord)
      last_master_poll.Set(time.Now().Unix())
      master_value = r.txtrecord
      master_queried_at = r.queried_at
    case r := <-slave_responses:
      // Make sure we have a FH open for this slave
      if _, ok := files[r.hostport]; !ok {
        filename := path.Join(output_dir, fmt.Sprintf("%v.data", r.hostport))
        f, err := os.OpenFile(filename, filemode, 0600)
        if err != nil {
          log.Fatal("Could not write to the output file:", err)
        }
        defer f.Close()
        files[r.hostport] = f  // store it for later use
      }

      // Drop polls from the slaves if the master data goes stale
      if r.queried_at > (master_queried_at + master_skew) {
        log.Printf("Master data too stale for this poll: %s @ %d", r.hostport,
                    r.queried_at, master_queried_at)
        continue
      }
      if r.queried_at < (master_queried_at - master_skew) {
        log.Println("Slave data too old for this poll: ", r.hostport,
                    r.queried_at, master_queried_at)
        continue
      }

      // if available, compare the response with the master, and log it
      latency := "nan"
      if r.txtrecord != 0 {
        latency = fmt.Sprintf("%d", master_value - r.txtrecord)
      }
      text := fmt.Sprintf("%d %.3f %s\n", r.queried_at, r.query_ms, latency)
      log.Printf("%-25s %s", r.hostport, text)
      if _, err := files[r.hostport].WriteString(text); err != nil {
        log.Printf("Could not write to %s log: %s", r.hostport, err)
      }
    }
  }

}


func slavesHandler(w http.ResponseWriter, req *http.Request) {
	//w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	fmt.Fprintf(w, "{\n\"slaves\": [")
  for i, hostport := range slaves {
    fmt.Fprintf(w, "\"%s\"", hostport)
    if i != len(slaves)-1 {
      fmt.Fprintf(w, ", ")
    }
  }
  fmt.Fprintf(w, "]\n}")
}


func graphHandler(w http.ResponseWriter, req *http.Request) {
  if err := req.ParseForm(); err != nil {
    http.Error(w, "Invalid request", 500)
    return
  }
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
  fmt.Fprintf(w, "%s", req.Form["test"])
  // TODO: return json usable by jQuery + chart api like this:
  // https://developers.google.com/chart/interactive/docs/php_example
  // <script src="//ajax.googleapis.com/ajax/libs/jquery/1.10.1/jquery.min.js"></script>
  /*
	fmt.Fprintf(w, "{\n\"slaves\": [")
  for i, hostport := range slaves {
    fmt.Fprintf(w, "\"%s\"", hostport)
    if i != len(slaves)-1 {
      fmt.Fprintf(w, ", ")
    }
  }
  fmt.Fprintf(w, "]\n}")
  */
}


func main() {
  dns_query := dns.Msg{}
  dns_query.SetQuestion("speedy.gonzales.joyner.ws.", dns.TypeTXT)
  var output_dir = "data"

  // Process the config, store the entries in a global []string
  config_filehandle, err := os.Open("dnsprobe.cfg")
  if err != nil {
    log.Fatal("error opening the config file: ", err)
  }
  bufScanner := bufio.NewScanner(config_filehandle)
  for bufScanner.Scan() {
    hostport := bufScanner.Text()
    if ! strings.Contains(hostport, ":") {
      log.Fatal("Config line does not contain a colon: ", hostport)
    }
    slaves = append(slaves, hostport)
  }
  config_filehandle.Close()

  // some channels for the master and slaves to coordinate later
  master_responses := make(chan Response, 3)
  slave_responses := make(chan Response, 100)

  // Poll the master to keep track of it's state
  // TODO: Accept the master address via a flag or config, with a default
  master := DnsServer{hostport: "68.115.138.202:53",
                      responses: master_responses,
                      dns_query: &dns_query}

  go master.pollmaster()
  // wait until the master has gotten a valid response, then re-enqueue it
  buffer := <-master_responses
  master_responses <-buffer

  // fire up one goroutine per slave to be polled
  for _, hostport := range slaves {
    dns_server := DnsServer{hostport: hostport,
                            responses: slave_responses,
                            dns_query: &dns_query}
    go dns_server.pollslave()

  }

  // Collate the responses and record them on disk
  go compare_responses(output_dir, master_responses, slave_responses)

  // launch the http server
  // TODO: build minimal web output that displays graphs
  http.HandleFunc("/slaves", slavesHandler)
  http.HandleFunc("/graph", graphHandler)
  log.Fatal(http.ListenAndServe(":8080", nil))
}
