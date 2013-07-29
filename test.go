package main

import "fmt"
import "time"

type Msg struct {
  happy string
}

func muhtest(c *[]Msg) {
    r := make([]Msg, 2)
    r[0].happy = "Yes"
    //c = &r  // doesn't work
    copy(*c, r) // works but is inefficient and not atomic
    //return c  // works if you return the value
}

func muhprinter(c *[]Msg) {
  r := *c
  fmt.Println(r[0].happy)
}

func main() {
  c := make([]Msg, 2)
  go muhtest(&c)
  sleepy_channel := time.After(1 * time.Second) // TODO: 300 seconds
  <-sleepy_channel
  muhprinter(&c)
}
