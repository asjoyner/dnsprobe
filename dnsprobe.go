package main

import (
  "bufio"
  "expvar"
  "flag"
  "fmt"
  "io/ioutil"
  "log"
  "net/http"
  "os"
  "os/exec"
  "path"
  "strconv"
  "strings"
  "text/template"
  "time"
  "github.com/tonnerre/godns"
)

var slaves []string
var hostname, output_dir string
var gitNow chan bool

var uploadToGit = flag.Bool("u", false, "Upload the dns probe data to github.")
var initialUploadDelay = flag.Duration("gitdelay", 300 * time.Second, "How long to wait before the first upload to github.")
var UploadDelay = flag.Duration("gitfreq", 3600 * time.Second, "How long to wait between subsequent uploads to github.")
var masterPollInterval = flag.Duration("m", 5 * time.Second, "How often to poll the master.  Please provide units.")
var slavePollInterval = flag.Duration("s", 30 * time.Second, "How often to poll the master.  Please provide units.")
var dnsPollTimeout = flag.Duration("t", 10 * time.Second, "How often to wait for a DNS response.")
var bindAddr = flag.String("address", "127.0.0.1:8080", "IP and port to bind HTTP server to.  Pass an empty string to disable the HTTP server.")

type DnsServer struct {
  hostport string
  file *os.File
  dns_client dns.Client
  dns_query *dns.Msg
  responses chan *Response
}

type Response struct {
  hostport string
  txtrecord, queried_at int64
  query_ms float64
}

// query once and pass the response down the responses channel
func (s *DnsServer) query() float64 {
  t := time.Now()
  var txtrecord int64 = 0
  var query_ms float64
  var err error // to match txtrecord, below
  resp, rtt, err := s.dns_client.Exchange(s.dns_query, s.hostport)
  if err != nil {
    log.Printf("Error reading from %s: %s (%s)", s.hostport, err, rtt)
    query_ms = float64(time.Since(t)) / 1000000
    txtrecord = -1
  } else if resp == nil || len(resp.Answer) == 0 {
    // We received an empty respose
    log.Printf("empty response from %s", s.hostport)
    query_ms = float64(time.Since(t)) / 1000000
  } else {
    // Parse the response into an int64
    query_ms = float64(rtt) / 1000000
    slave_response := resp.Answer[0].(*dns.TXT).Txt[0]
    txtrecord, err = strconv.ParseInt(slave_response, 10, 0)
    if err != nil {
      log.Printf("Bad data from %s: %s", s.hostport, err)
      txtrecord = -2
    }
  }

  s.responses <- &Response{s.hostport, txtrecord, t.Unix(), query_ms}
  return query_ms
}


// loop forever polling the slave
// TODO: integrate slave and master versions, export varz for slaves
func (s *DnsServer) pollslave() {
  dns_client := &dns.Client{}
  dns_client.ReadTimeout = *dnsPollTimeout
  s.dns_client = *dns_client
  ticker := time.NewTicker(*slavePollInterval)
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
  dns_client.ReadTimeout = *dnsPollTimeout
  s.dns_client = *dns_client
  ticker := time.NewTicker(*masterPollInterval)
  for {
    // Send the query to the master
    <-ticker.C
    query_ms := s.query()

    // Notify the main thread that we've made a successful first poll
    queries_sent++
    queries_sent_varz.Set(queries_sent)
    cumulative_latency = cumulative_latency + query_ms
    queries_avg = cumulative_latency / float64(queries_sent)
    queries_avg_varz.Set(queries_avg)
  }
}

func compare_responses(master_responses, slave_responses chan *Response) {
  files := make(map[string]*os.File)
  filemode := os.O_CREATE|os.O_APPEND|os.O_WRONLY
  var master_value, master_queried_at int64
  var last_master_poll = expvar.NewInt("last-successful-master-poll")
  var master_skew = int64(*masterPollInterval)/1000000000 * 5

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
          log.Println("Could not write to the output file:", err)
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
      var latency string
      switch {
      case r.txtrecord > 0:
        latency = fmt.Sprintf("%d", master_value - r.txtrecord)
      case r.txtrecord == 0:
        latency = "nan"
      case r.txtrecord == -1:
        latency = "timeout"
      case r.txtrecord == -2:
        latency = "bogus"
      }
      text := fmt.Sprintf("%d %.3f %s\n", r.queried_at, r.query_ms, latency)
      //log.Printf("%-25s %s", r.hostport, text)
      if _, err := files[r.hostport].WriteString(text); err != nil {
        log.Printf("Could not write to %s log: %s", r.hostport, err)
      }
    }
  }

}

func autoUpdate() {
  ticker := time.NewTicker(3600 * time.Second)
  for {
    <-ticker.C
    resp, err := http.Get("http://joyner.ws/dnsprobe")
    if err != nil {
      log.Println("Autoupdate get failed: ", err)
      continue
    }
    defer resp.Body.Close()
    body, err := ioutil.ReadAll(resp.Body)
    if err != nil {
      log.Println("Reading binary failed: ", err)
      continue
    }
    log.Println(body)
    // TODO: Decide if it's worth it to autoupdate
  }
}


func backupResults () {
  if !*uploadToGit {
        log.Println("Not backing up data to github.")
        return
  }
  select {  // wait for http signal of gitNow or the timeout to expire
  case <-gitNow:
  case <-time.After(*initialUploadDelay):
  }
  log.Println("Preparing to backup data to github.")
  ticker := time.NewTicker(*UploadDelay)
  var err error
  var output []byte
  for {
    err = nil
    comment := fmt.Sprintf("Automatic submission by %s", hostname)
    git_commands := [][]string{
      {"add", "."},
      {"commit", "-am", comment},
      {"pull", "--rebase"},
      {"push"},
    }
    for _, args := range git_commands {
      cmd := exec.Command("git", args...)
      cmd.Dir = output_dir
      output, err = cmd.Output()
      if err != nil {
        log.Printf("Failed to call git %s: %s\n", args, output)
        break
      }
    }
    if err == nil {
      log.Printf("Completed successful backup to github.")
    }
    select {  // wait for http signal of gitNow or the timeout to expire
    case <-gitNow:
    case <-ticker.C:
    }

  }
}


func getJsonForSlave(slave string) (string, error) {
  //var points *[]int
  filename := path.Join(output_dir, fmt.Sprintf("%v.data", slave))
  f, err := os.Open(filename)
  if err != nil {
    log.Println("Could not read from slave file for http request:", err)
    return "", err
  }
  defer f.Close()
  // TODO: read the file, convert to JSON, return as string
  scanner := bufio.NewScanner(f)
  for scanner.Scan() {
    line := strings.Split(scanner.Text(), " ")
    if len(line) < 3 {
      continue
    }
    //if line[1] // atoi this
  }
  return "", err
}


func slavesHandler(w http.ResponseWriter, req *http.Request) {
	//w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	fmt.Fprintf(w, "[")
  for i, hostport := range slaves {
    fmt.Fprintf(w, "\"%s\"", hostport)
    if i != len(slaves)-1 {
      fmt.Fprintf(w, ", ")
    }
  }
  fmt.Fprintf(w, "]\n")
}


func graphHandler(w http.ResponseWriter, req *http.Request) {
  if err := req.ParseForm(); err != nil {
    http.Error(w, "I could not parse your form variables.", 500)
    return
  }
  if _, err := req.Form["slave"]; ! err {
    http.Error(w, "Please specify a slave.", 501)
    return
  }
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
  for _, slave := range slaves {
    if slave == req.Form.Get("slave") {
    }
  }
  fmt.Fprintf(w, "Coming soon!")
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


func gitNowHandler(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "Triggering an upload to git now...")
  gitNow <- true
  return
}


func rootHandler(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
  root, err := template.New("index.html").ParseFiles("html/index.html")
  if err != nil { panic(err) }
  err = root.Execute(w, slaves)
  if err != nil { panic(err) }
}


func launchHttpServer() {
  // TODO: build minimal web output that displays graphs
  http.HandleFunc("/", rootHandler)
  http.HandleFunc("/upload", gitNowHandler)
  http.HandleFunc("/slaves", slavesHandler)
  http.HandleFunc("/graph", graphHandler)
	http.Handle("/html/", http.StripPrefix("/html/",
              http.FileServer(http.Dir("html"))))
  log.Fatal(http.ListenAndServe(*bindAddr, nil))
}


func main() {
  flag.Parse()
  dns_query := dns.Msg{}
  dns_query.SetQuestion("speedy.gonzales.joyner.ws.", dns.TypeTXT)
  gitNow = make(chan bool)

  // Setup the initial environment
  resp, err := os.Hostname()
  if err != nil {
    log.Fatal("Can not determine the machine's hostname: %s", err)
  }
  hostname = resp
  output_dir = path.Join("dnsprobe-data", hostname)
  log.Printf("Will write log data to: %s/\n", output_dir)
  os.MkdirAll(output_dir, 0775)
  if _, err := os.Stat(output_dir); err != nil {
      if os.IsNotExist(err) {
        log.Fatal("The log data directory could not be created.")
      } else {
        log.Fatal("Could not check on the state of the log dir?: %s", err)
      }
  }
  // Keep the server up to date, maybe.. eventually...
  //go autoUpdate()

  // Process the config, store the entries in a global []string
  config_filehandle, err := os.Open("dnsprobe.cfg")
  if err != nil {
    log.Fatal("error opening the config file: ", err)
  }
  scanner := bufio.NewScanner(config_filehandle)
  for scanner.Scan() {
    hostport := scanner.Text()
    if ! strings.Contains(hostport, ":") {
      log.Fatal("Config line does not contain a colon: ", hostport)
    }
    slaves = append(slaves, hostport)
  }
  config_filehandle.Close()

  // configure and launch the http server
  if *bindAddr != "" {
    go launchHttpServer()
  }

  // some channels for the master and slaves to coordinate later
  master_responses := make(chan *Response, 3)
  slave_responses := make(chan *Response, 100)

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
  go compare_responses(master_responses, slave_responses)

  // Periodically copy the results up to github
  go backupResults()

  // Hang out for a little while
  for {
    <-time.After(8760 * time.Hour)
  }
}
