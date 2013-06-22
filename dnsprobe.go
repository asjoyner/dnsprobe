package main

import (
  "bufio"
  "fmt"
  "log"
  "os"
  "path"
  "sync"
  "time"
  "github.com/tonnerre/godns"
)

type DnsServer struct {
  ipaddr, output_dir string
  file *os.File
  dns_client dns.Client
  dns_query *dns.Msg
  master_response *[]dns.RR
  wait_group sync.WaitGroup
}


func query(dns_client dns.Client, host string, dns_query *dns.Msg) []dns.RR {
  resp, err := dns_client.Exchange(dns_query, host)
  if err != nil {
    fmt.Println(err)
    return nil
  }
  return resp.Answer
}


// query once and write the output to the provided file handle
func (dns_server *DnsServer) recordquery() {
  t := time.Now()
  // TODO: get the return value, compare with master_response
  // TODO: check_current_correct()
  // TODO: if changed and configured: check_convergence()
  query(dns_server.dns_client, dns_server.ipaddr, dns_server.dns_query)
  query_time := float64(time.Since(t) / time.Microsecond)

  text := fmt.Sprintf("%d %.3f\n", t.Unix(), query_time / 1000)

  if _, err := dns_server.file.WriteString(text); err != nil {
    panic(err)
  }
}


// open the output file, loop forever polling the slave
func (dns_server *DnsServer) pollslave() {
  filename := path.Join(dns_server.output_dir,
                        fmt.Sprintf("%v.data", dns_server.ipaddr))
  f, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
  if err != nil {
    log.Fatal("Could not write to the output file:", err)
  }
  defer f.Close()
  dns_server.file = f

  dns_client := &dns.Client{}
  dns_client.ReadTimeout = 3 * time.Second  // TODO: 30 seconds
  dns_server.dns_client = *dns_client
  for {
    // Schedule the wakeup before sending the query, so RTT doesn't cause skew
    sleepy_channel := time.After(5 * time.Second) // TODO: 300 seconds
    dns_server.recordquery()
    <-sleepy_channel
  }
  dns_server.wait_group.Done()
}


func (dns_server *DnsServer) pollmaster() {
  dns_client := &dns.Client{}
  dns_client.ReadTimeout = 3 * time.Second  // TODO: 30 seconds
  dns_server.dns_client = *dns_client
  for {
    // Schedule the wakeup before sending the query, so RTT doesn't cause skew
    sleepy_channel := time.After(5 * time.Second) // TODO: 300 seconds
    resp := query(dns_server.dns_client, dns_server.ipaddr,
                  dns_server.dns_query)
    if resp != nil {
      dns_server.master_response = &resp
    }
    <-sleepy_channel
  }
  dns_server.wait_group.Done()
}


func main() {
  // Allocate those globals
  dns_query := dns.Msg{}
  dns_query.SetQuestion("www.joyner.ws.", dns.TypeANY)
  var wait_group sync.WaitGroup

  config_filehandle, err := os.Open("dnsprobe.cfg")
  if err != nil {
    log.Fatal("error opening the config file: ", err)
  }

  var master_response *[]dns.RR
  // Poll the master to keep track of it's state
  // TODO: Runtime panic if ipaddr has no semicolon.  Check for that.
  master := DnsServer{ipaddr: "4.3.2.1:53",
                      master_response: master_response,
                      dns_query: &dns_query,
                      wait_group: wait_group}

  wait_group.Add(1)
  go master.pollmaster()

  bufScanner := bufio.NewScanner(config_filehandle)
  for bufScanner.Scan() {
    dns_server := DnsServer{ipaddr: bufScanner.Text(),
                            output_dir: "data",
                            master_response: master_response,
                            dns_query: &dns_query,
                            wait_group: wait_group}

    wait_group.Add(1)
    go dns_server.pollslave()
  }

  // TODO: a switch here that will also await signals and handle them?
  wait_group.Wait()
  /*
  t := time.Now()
  resp1 := query("128.0.0.1:53")
  resp2 := query("127.0.0.1:53")
  // TODO: Find or write a good way to compare these
  if resp1 == nil || resp2 == nil {
    fmt.Println("Boo!")
  } else if len(resp1) == len(resp2) {
    fmt.Println("Hooray!")
  } else {
    fmt.Println("Boo!")
  }
  
  //
  resp, err := c.Exchange(m, "127.0.0.1:53")
  if err != nil {
    fmt.Println("Whoops...")
  } else {
    for _, rr := range resp.Answer {  // iterate the returned records
      fmt.Println(rr.Header().Name)
    }
  }
  */
  //fmt.Println("Happy Day")
  //time.Sleep(300 * time.Millisecond)
  //fmt.Println(time.Since(t))
}
