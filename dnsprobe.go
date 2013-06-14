package main

import (
  "bufio"
  "fmt"
  "log"
  "os"
  "path"
  "time"
  "github.com/tonnerre/godns"
)

// Define some globals that will get used over and over
var dns_query *dns.Msg

func query(dns_client *dns.Client, host string) []dns.RR {
  resp, err := dns_client.Exchange(dns_query, host)
  if err != nil {
    fmt.Println(err)
    return nil
  }
  return resp.Answer
}


// query once and write the output to the provided file handle
func recordquery(dns_client *dns.Client, host string, f *os.File) {
  t := time.Now()
  // TODO: get the return value, sanity check it
  query(dns_client, host)
  query_time := float64(time.Since(t) / time.Microsecond)

  text := fmt.Sprintf("%d %.3f\n", t.Unix(), query_time / 1000)

  if _, err := f.WriteString(text); err != nil {
    panic(err)
  }
}


// open the output file, loop forever polling the slave
func pollslave(host string, output_dir string) {
  filename := path.Join(output_dir, fmt.Sprintf("%v.data", host))
  f, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
  if err != nil {
    log.Fatal("Could not write to the output file:", err)
  }
  defer f.Close()

  dns_client := &dns.Client{}
  // TODO: Set the read timeout to 30 before entering production
  dns_client.ReadTimeout = 3 * time.Second
  for {
    // TODO: Schedule the wakeup before calling recordquery, so we poll on
    // consistent intervals intead of query-timeout + sleep interval
    recordquery(dns_client, host, f)
    <-time.After(5 * time.Second)
  }
}


func main() {
  // Allocate those globals
  dns_query = &dns.Msg{}
  dns_query.SetQuestion("www.joyner.ws.", dns.TypeA)

  config_filehandle, err := os.Open("dnsprobe.cfg")
  if err != nil {
    log.Fatal("error opening the config file: ", err)
  }

  bufScanner := bufio.NewScanner(config_filehandle)
  for bufScanner.Scan() {
    go pollslave(bufScanner.Text(), "data")
  }

  for {
    // TODO: Do something moar interesting...
    <-time.After(300 * time.Second)
  }

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
