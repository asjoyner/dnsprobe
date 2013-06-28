package main

import (
  "bufio"
  "fmt"
  "log"
  "os"
  "path"
  "strings"
  "sync"
  "time"
  "reflect"
  "github.com/tonnerre/godns"
)

type DnsServer struct {
  ipaddr, output_dir string
  file *os.File
  dns_client dns.Client
  dns_query *dns.Msg
  master_response *[]dns.RR
  wait_group *sync.WaitGroup
}

// TODO: Need a custom comparator which ignores the TTL which should differ
/*func compare_rr(p1 dns.RR[], p2 dns.RR[]) bool {
  if t, ok := p1.Answer[0].(*dns.TXT); ok {
  }
  return false
}*/

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
  if reflect.DeepEqual(*s.master_response, resp.Answer) {
    //log.Printf("Response is the same from %s: %+v vs %+v", s.ipaddr,
    //           *s.master_response, resp.Answer)
    equal = 1
  } else {
    log.Printf("Response is different from %s: %+v vs %+v", s.ipaddr,
               *s.master_response, resp.Answer)
  }
  query_time := float64(rtt / time.Microsecond)

  // TODO: get_ttl(resp.Answer)

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
  for {
    // Schedule the wakeup before sending the query, so RTT doesn't cause skew
    sleepy_channel := time.After(5 * time.Second) // TODO: 300 seconds
    s.recordquery()
    <-sleepy_channel
  }
  s.wait_group.Done()
}


func (s *DnsServer) pollmaster() {
  var once sync.Once
  dns_client := &dns.Client{}
  dns_client.ReadTimeout = 3 * time.Second  // TODO: 30 seconds
  s.dns_client = *dns_client
  for {
    // Schedule the wakeup before sending the query, so RTT doesn't cause skew
    sleepy_channel := time.After(5 * time.Second) // TODO: 300 seconds
    resp, _, _ := s.dns_client.Exchange(s.dns_query, s.ipaddr)
    if resp != nil {
      if !reflect.DeepEqual(*s.master_response, resp.Answer) {
        log.Printf("Received updated master response: %s", resp.Answer)
        // TODO: Do this w/o the copy, which is racy and ugly.
        copy(*s.master_response, resp.Answer)
      }
      // Notify the main thread that we've made a successful first poll
      once.Do(s.wait_group.Done)
    }
    <-sleepy_channel
  }
}


func main() {
  dns_query := dns.Msg{}
  dns_query.SetQuestion("speedy.gonzales.joyner.ws.", dns.TypeTXT)
  var wait_group sync.WaitGroup

  config_filehandle, err := os.Open("dnsprobe.cfg")
  if err != nil {
    log.Fatal("error opening the config file: ", err)
  }

  master_response := make([]dns.RR, 1)

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
