package main

import (
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
  query_time := uint64(time.Since(t) / time.Microsecond)

  text := fmt.Sprintf("%d %d\n", t.Unix(), query_time)

  if _, err := f.WriteString(text); err != nil {
        panic(err)
  }
}


// open the output file, loop forever polling the slave
func pollslave(host string, output_dir string) {
  filename := path.Join(output_dir, "data", fmt.Sprintf("%v.data", host))
  f, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
  if err != nil {
        log.Fatal(err)
  }
  defer f.Close()
  
  dns_client := &dns.Client{}
  for {
    recordquery(dns_client, host, f)
    <-time.After(5 * time.Second)
  }
}


func main() {
  // Allocate those globals
  dns_query = &dns.Msg{}
  dns_query.SetQuestion("www.joyner.ws.", dns.TypeA)

  go pollslave("127.0.0.1:53", ".")
  pollslave("10.0.5.1:53", ".")

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
