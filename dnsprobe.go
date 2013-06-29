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

type DnsServer struct {
  ipaddr, output_dir string
  file *os.File
  dns_client dns.Client
  dns_query *dns.Msg
  master_response *int
  wait_group *sync.WaitGroup
}

// query once and write the output to the provided file handle
func (s *DnsServer) recordquery() {
  t := time.Now()
  var equal int
  resp, rtt, err := s.dns_client.Exchange(s.dns_query, s.ipaddr)
  if err != nil {
    log.Printf("Error reading from %s: %s (%s)", s.ipaddr, err, rtt)
    // TODO: Write out the 'nan' value
    return
  }

  // Slave returned empty respose
  if resp == nil || len(resp.Answer) == 0 {
    log.Printf("slave returned empty response %s", s.ipaddr)
    // TODO: Write out a 'nan' value
    return
  }

  // Parse the slave's response
  slave_response, err := strconv.Atoi(resp.Answer[0].(*dns.TXT).Txt[0])
  if err != nil {
    log.Printf("Bad data from slave: %s", s.ipaddr, err)
    // TODO: Write out the 'nan' value
    return
  }

  if *s.master_response <= slave_response {
    log.Printf("Response is the same from %s: %+v vs %+v", s.ipaddr,
               *s.master_response, slave_response)
    equal = 1
  } else {
    log.Printf("Response is different from %s: %+v vs %+v", s.ipaddr,
               *s.master_response, slave_response)
  }
  query_time := float64(rtt / time.Microsecond)

  text := fmt.Sprintf("%d %.3f %d\n", t.Unix(), query_time / 1000, equal)
  if _, err := s.file.WriteString(text); err != nil {
    panic(err)  // TODO: Don't panic here, just log an error
  }
}


// open the output file, loop forever polling the slave
func (s *DnsServer) pollslave() {
  filename := path.Join(s.output_dir, fmt.Sprintf("%v.data", s.ipaddr))
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
  ticker := time.NewTicker(5 * time.Second) // TODO: 300 seconds
  for {
    <-ticker.C  // Don't send a flood of queries in a restart loop...
    // Send the query to the master
    resp, _, err := s.dns_client.Exchange(s.dns_query, s.ipaddr)
    if resp == nil {
      log.Printf("master returned empty response %s: %d", s.ipaddr, err)
      continue
    }

    // Parse the master's response
    master_response, err := strconv.Atoi(resp.Answer[0].(*dns.TXT).Txt[0])
    if err != nil {
      log.Printf("Bad data from master %s: %s (%s)", s.ipaddr, err,
                 resp.Answer[0].(*dns.TXT).Txt[0])
      continue
    }

    // Notify the main thread that we've made a successful first poll
    if *s.master_response != master_response {
      log.Printf("New data from master %s: %d vs %d", s.ipaddr,
                 *s.master_response, master_response)
    }
    s.master_response = &master_response
    once.Do(s.wait_group.Done)
  }
}


func main() {
  dns_query := dns.Msg{}
  dns_query.SetQuestion("speedy.gonzales.joyner.ws.", dns.TypeTXT)
  var wait_group sync.WaitGroup
  var master_response int

  config_filehandle, err := os.Open("dnsprobe.cfg")
  if err != nil {
    log.Fatal("error opening the config file: ", err)
  }

  // TODO: Process the config before making the first query to the master?

  // Poll the master to keep track of it's state
  // TODO: Accept the master address via a flag or config, with a default
  master := DnsServer{ipaddr: "68.115.138.202:53",
                      master_response: &master_response,
                      dns_query: &dns_query,
                      wait_group: &wait_group}

  wait_group.Add(1)
  go master.pollmaster()
  wait_group.Wait()  // wait until the master has gotten a valid response

  bufScanner := bufio.NewScanner(config_filehandle)
  for bufScanner.Scan() {
    ipaddr_with_port := bufScanner.Text()
    if ! strings.Contains(ipaddr_with_port, ":") {
      log.Fatal("Config line does not contain a colon: ", ipaddr_with_port)
    }
    dns_server := DnsServer{ipaddr: ipaddr_with_port,
                            output_dir: "data",
                            master_response: &master_response,
                            dns_query: &dns_query,
                            wait_group: &wait_group}

    wait_group.Add(1)
    go dns_server.pollslave()
  }

  // TODO: a switch here that will also await signals and handle them?
  wait_group.Wait()
}
