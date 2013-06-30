package main

import (
  "bufio"
  "fmt"
  "log"
  "os"
  "path"
  "strconv"
  "strings"
  "sync"
  "time"
  "github.com/tonnerre/godns"
)

var MASTER_POLL_INTERVAL = 1 * time.Second

type DnsServer struct {
  hostport, output_dir string
  file *os.File
  dns_client dns.Client
  dns_query *dns.Msg
  responses chan Response
  wait_group *sync.WaitGroup
}

type Response struct {
  hostport string
  txtrecord, queried_at int64
  query_ms float64
}

// query once and write the output to the provided file handle
func (s *DnsServer) recordquery() {
  t := time.Now()
  resp, rtt, err := s.dns_client.Exchange(s.dns_query, s.hostport)
  if err != nil {
    log.Printf("Error reading from %s: %s (%s)", s.hostport, err, rtt)
    // TODO: Write out the 'nan' value
    return
  }

  // Slave returned empty respose
  if resp == nil || len(resp.Answer) == 0 {
    log.Printf("slave returned empty response %s", s.hostport)
    return
  }

  // Parse the slave's response into an int64
  slave_response := resp.Answer[0].(*dns.TXT).Txt[0]
  txtrecord, err := strconv.ParseInt(slave_response, 10, 0)
  if err != nil {
    log.Printf("Bad data from slave: %s", s.hostport, err)
    // TODO: Write out the 'nan' value
    return
  }

  query_ms := float64(rtt) / 1000000
  query_ms = float64(time.Since(t)) / 1000000
  s.responses <- Response{s.hostport, txtrecord, t.Unix(), query_ms}

}


// open the output file, loop forever polling the slave
func (s *DnsServer) pollslave() {
  filename := path.Join(s.output_dir, fmt.Sprintf("%v.data", s.hostport))
  f, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
  if err != nil {
    log.Fatal("Could not write to the output file:", err)
  }
  defer f.Close()
  s.file = f

  dns_client := &dns.Client{}
  dns_client.ReadTimeout = 3 * time.Second  // TODO: 30 seconds
  s.dns_client = *dns_client
  ticker := time.NewTicker(5 * time.Second) // TODO: 300 seconds
  for {
    s.recordquery()
    <-ticker.C
  }
  s.wait_group.Done()
}


func (s *DnsServer) pollmaster() {
  var once sync.Once
  dns_client := &dns.Client{}
  dns_client.ReadTimeout = 3 * time.Second  // TODO: 30 seconds
  s.dns_client = *dns_client
  ticker := time.NewTicker(MASTER_POLL_INTERVAL) // TODO: 300 seconds
  for {
    <-ticker.C  // Don't send a flood of queries in a restart loop...
    // Send the query to the master
    t := time.Now()
    resp, rtt, err := s.dns_client.Exchange(s.dns_query, s.hostport)
    if resp == nil {
      log.Printf("master returned empty response %s: %d", s.hostport, err)
      continue
    }

    // Parse the master's response into an int64
    master_response := resp.Answer[0].(*dns.TXT).Txt[0]
    txtrecord, err := strconv.ParseInt(master_response, 10, 0)
    if err != nil {
      log.Printf("Bad data from master %s: %s (%s)", s.hostport, err,
                 resp.Answer[0].(*dns.TXT).Txt[0])
      continue
    }

    // Update the compare_responses() routine
    query_ms := float64(rtt) / 100000
    s.responses <- Response{s.hostport, txtrecord, t.Unix(), query_ms}

    // Notify the main thread that we've made a successful first poll
    once.Do(s.wait_group.Done)
  }
}

// TODO: why can't I combine the declaration of the responses?
func compare_responses(output_dir string, master_responses chan Response,
                       slave_responses chan Response) {
  files := make(map[string]*os.File)
  filemode := os.O_CREATE|os.O_APPEND|os.O_WRONLY
  var master_value, master_queried_at int64

  for {
    select {
    case r := <-master_responses:
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
      if r.queried_at > (master_queried_at + int64(MASTER_POLL_INTERVAL)*5) {
        log.Println("Master data too stale for this poll: %s @ %d", r.hostport,
                    r.queried_at, master_queried_at)
      }
      if r.queried_at < (master_queried_at - int64(MASTER_POLL_INTERVAL)*5) {
        log.Println("Slave data too old for this poll: ", r.hostport,
                    r.queried_at, master_queried_at)
      }

      // if available, compare it's response with the master, and log it
      latency := "nan"
      if r.txtrecord != 0 {
        latency = fmt.Sprintf("%d", master_value - r.txtrecord)
      }
      text := fmt.Sprintf("%d %.3f %s\n", r.queried_at, r.query_ms, latency)
      log.Printf("%-25s %s", r.hostport, text)
      if _, err := files[r.hostport].WriteString(text); err != nil {
        panic(err)  // TODO: Don't panic here, just log an error
      }
    }
  }

}

func main() {
  dns_query := dns.Msg{}
  dns_query.SetQuestion("speedy.gonzales.joyner.ws.", dns.TypeTXT)
  var wait_group sync.WaitGroup
  var output_dir = "data"

  config_filehandle, err := os.Open("dnsprobe.cfg")
  if err != nil {
    log.Fatal("error opening the config file: ", err)
  }


  // TODO: Process the config before making the first query to the master?

  // Setup some channels for the master and slaves to coordinate later
  master_responses := make(chan Response, 3)
  slave_responses := make(chan Response, 100)

  // Poll the master to keep track of it's state
  // TODO: Accept the master address via a flag or config, with a default
  master := DnsServer{hostport: "68.115.138.202:53",
                      responses: master_responses,
                      dns_query: &dns_query,
                      wait_group: &wait_group}

  wait_group.Add(1)
  go master.pollmaster()
  wait_group.Wait()  // wait until the master has gotten a valid response

  bufScanner := bufio.NewScanner(config_filehandle)
  for bufScanner.Scan() {
    hostport := bufScanner.Text()
    if ! strings.Contains(hostport, ":") {
      log.Fatal("Config line does not contain a colon: ", hostport)
    }
    dns_server := DnsServer{hostport: hostport,
                            responses: slave_responses,
                            dns_query: &dns_query,
                            wait_group: &wait_group}

    wait_group.Add(1)
    go dns_server.pollslave()

  }

  // Collate the responses and record them on disk
  compare_responses(output_dir, master_responses, slave_responses)

  // TODO: a switch here that will also await signals and handle them?
  wait_group.Wait()
}
