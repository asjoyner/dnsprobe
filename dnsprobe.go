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
  hostname, output_dir string
  file *os.File
  master bool
  dns_client dns.Client
  dns_query *dns.Msg
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
  // TODO: get the return value, compare with current value
  // TODO: check_current_correct()
  // TODO: if changed and configured: check_convergence()
  query(dns_server.dns_client, dns_server.hostname, dns_server.dns_query)
  query_time := float64(time.Since(t) / time.Microsecond)

  text := fmt.Sprintf("%d %.3f\n", t.Unix(), query_time / 1000)

  if _, err := dns_server.file.WriteString(text); err != nil {
    panic(err)
  }
}


// open the output file, loop forever polling the slave
func (dns_server *DnsServer) pollslave() {
  filename := path.Join(dns_server.output_dir,
                        fmt.Sprintf("%v.data", dns_server.hostname))
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


func main() {
  // Allocate those globals
  dns_query := dns.Msg{}
  dns_query.SetQuestion("www.joyner.ws.", dns.TypeANY)
  var wait_group sync.WaitGroup

  config_filehandle, err := os.Open("dnsprobe.cfg")
  if err != nil {
    log.Fatal("error opening the config file: ", err)
  }

  bufScanner := bufio.NewScanner(config_filehandle)
  for bufScanner.Scan() {
    wait_group.Add(1)
    dns_server := DnsServer{hostname: bufScanner.Text(),
                            output_dir: "data",
                            dns_query: &dns_query,
                            wait_group: wait_group}

    go dns_server.pollslave()
  }

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
