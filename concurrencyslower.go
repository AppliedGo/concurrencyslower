/*
<!--
Copyright (c) 2019 Christoph Berger. Some rights reserved.

Use of the text in this file is governed by a Creative Commons Attribution Non-Commercial
Share-Alike License that can be found in the LICENSE.txt file.

Use of the code in this file is governed by a BSD 3-clause license that can be found
in the LICENSE.txt file.

The source code contained in this file may import third-party source code
whose licenses are provided in the respective license files.
-->

<!--
NOTE: The comments in this file are NOT godoc compliant. This is not an oversight.

Comments and code in this file are used for describing and explaining a particular topic to the reader. While this file is a syntactically valid Go source file, its main purpose is to get converted into a blog article. The comments were created for learning and not for code documentation.
-->

+++
title = "Slow down your code with goroutines"
description = "Concurrent code can be slower than its serial counterpart due to CPU cache synchronization"
author = "Christoph Berger"
email = "chris@appliedgo.net"
date = "2019-04-10"
draft = "true"
categories = ["Concurrent Programming"]
tags = ["goroutine", "cacheline", "cpu architecture"]
articletypes = ["Tutorial"]
+++

Or: How adding goroutines can keep your CPU busy shuffling things around.

<!--more-->

Here, a large loop! Let's chop the input into pieces and the loop into goroutines!

I bet you had this situation (and feeling) a few times already, but did it make your code faster every time? Here is an example of a simple loop that seems easy to turn into concurrent code - but as we will see, the concurrent version will not just be not faster, it actually will take double the time.

## The serial loop

Our starting point is a simple serial loop that does nothing but summing up the loop index.

*/

// Imports and globals
package concurrencyslower

import (
	"runtime"
	"sync"
)

const (
	limit = 10000000000
)

// `SerialSum` sums up all numbers from 0 to limit, sice and easy!
func SerialSum() int {
	sum := 0
	for i := 0; i < limit; i++ {
		sum += i
	}
	return sum
}

/*
## The concurrent variant

Obviously, this loop will only occupy a single (logical) CPU core. The natural Gopher reflex is thus to break this into goroutines. Goroutines are functions that can run independent from the rest of the code and hence can be distributed among all available CPU cores.
*/

// `ConcurrentSum` attempts to make use of all available cores.
func ConcurrentSum() int {
	// Get the number of available logical cores. Usually this is 2*c where c is the number of physical cores and 2 is the number of hyperthreads per core.
	n := runtime.GOMAXPROCS(0)

	// We need to collect the results from the `n` goroutines somewhere. How about a gloaal slice with one element for every goroutine.
	sums := make([]int, n)

	// Now we can spawn the goroutines. A `WaitGroup` helps us detecting when all goroutines have finished.
	wg := sync.WaitGroup{}
	for i := 0; i < n; i++ {

		// One Add() for each goroutine spawned.
		wg.Add(1)
		go func(i int) {
			// Split the "input" into n chunks that do not overlap.
			start := (limit / n) * i
			end := start + (limit / n)

			// Run the loop over the given chunk.
			for j := start; j < end; j += 1 {
				sums[i] += j
			}

			// Tell the `WaitGroup` that this goroutine has completet its task.
			wg.Done()
		}(i)
	}

	// Wait for all goroutines to call `Done()`.
	wg.Wait()

	// Collect the total sum from the sums of the n chunks.
	sum := 0
	for _, s := range sums {
		sum += s
	}
	return sum
}

/*
## The speed gain is... negative!

So how does the concurrent version compare to the serial one? A quick benchmark should show the gain we get.
A `*_test.go` file holds two simple benchmark functions, one for each of our two loop functions.

```go
package concurrencyslower

import "testing"

func BenchmarkSerialSum(b *testing.B) {
	for i := 0; i < b.N; i++ {
		SerialSum()
	}
}

func BenchmarkConcurrentSum(b *testing.B) {
	for i := 0; i < b.N; i++ {
		ConcurrentSum()
	}
}
```

My CPU is a small laptop CPU (two hyperthreaded cores, which the Go runtime sees as four logical cores), but still, the concurrent version should show a noticeable speed gain.

So le't run the benchmarks:

```sh
$ go test -bench .
goos: darwin
goarch: amd64
pkg: github.com/appliedgo/concurrencyslower
BenchmarkSerialSum-4           1      6090666568 ns/op
BenchmarkConcurrentSum-4       1      15741988135 ns/op
PASS
ok      github.com/appliedgo/concurrencyslower 21.840s
```

The prefix `-4` confirms that the test uses all four logical cores. But wait, what's that? The concurrent loop took **more than twice** the time as its serial counterpart. And that despite it used all four logical cores. What is going on here?


## Hardware acceleration fires back

To explain this counter-intuitive result, we have to take a look at what lies beneath all software---the CPU chip.

The problem starts at the point where cache memory helps speeding up each CPU core.

The following is a gross oversimplification for the sake of clarity and conciseness, so dear CPU designers, please be lenient towards me. Every modern CPU has a non-trivial hierarchy of caches that sit somewhere between main memory and the bare CPU cores, but for our purpose we will only look at the caches that belong to individual cores.

## The purpose of a CPU cache

A cache is, generally speaking, a very small but super fast memory block. It sits right on the CPU chip, so the CPU does not have to reach out to main RAM every time when reading or writing a value. Instead, the value is stored in the cache, and subsequent reads and writes benefit from faster RAM cells and shorter access routes.

Every core of a CPU has its own local cache that is not shared with any other core. For `n` CPU cores this means that there can be up to `n+1` copies of the same data; one in main memory, and one in every CPU core's cache.

Now when a CPU core changes a value in its local cache, it has to be synchronized back to main memory at some point. Likewise, if a cached value gets changed in main memory (by another CPU core), the cached value is invalid and needs to get refreshed from main memory.

!HYPE[cpucache](cpucache.html)


## The cacheline

To synchronize cache and main memory in an efficient way, data is synchronized in blocks of typically 64 bit. These blocks are called cachelines.

So when a cached value changes, the whole cacheline gets synchronized back to main memory. Likewise, the caches of all other CPU cores that contain this cacheline must now also sync this cacheline to avoid operating on outdated data.


## Neighborhood

How does this affect our code? Remember that the concurrent loop uses a global slice to store the intermediate results. The elements of a slice are stored in a contiguous space. With high probability, two adjacent slice elements will share the same cacheline.

And now the drama begins.

`n` CPU cores with `n` caches repeatedly read from and write to slice elements that are all in the same cacheline. So whenever one CPU core updates "its" slice element with a new sum, the cachelines of all other CPU's get invalidated. The changed cacheline must be written back to main memory, and all other caches must update their respective cacheline with new data. *Even though each core accesses a different part of the slice!*

This consumes precious time---more than the time that the serial loop needs for updating its single sum variable.

This is why our concurrent loop needs more time than the serial loop. All the concurrent updates to the slice cause a frantic cacheline sync dance.

!HYPE[syncdance](syncdance.html)


## Spread the ~~word~~ data!

Now that we know the reason for the surprsing slowdown, the cure is obvious. We have to turn the slice into `n` individual variables that hopefully will be stored far enough apart from each other so that they do not share the same cacheline.

So let's change our concurrent loop so that each goroutine stores its intermediate sum in a goroutine-local variable. In order to pass the result back to the main goroutine, we also have to add a channel. This in turn allows us to remove the wait group, because channels are not only a means for communication but also an elegant synchronization mechanism.


## Concurrent loop with local variables

*/

// `ChannelSum()` spawns `n` goroutines that store their intermediate sums locally, then pass the result back through a channel.
func ChannelSum() int {
	n := runtime.GOMAXPROCS(0)

	// A channel of ints will collect all intermediate sums.
	res := make(chan int)

	for i := 0; i < n; i++ {
		// The goroutine now receives a second parameter, the result channel. The arrow pointing "into" the `chan` keyword turns this channel into a send-only channel inside this function.
		go func(i int, r chan<- int) {
			// This local variable replaces the global slice.
			sum := 0
			// As before, we divide the input into `n` chunks of equal size.
			start := (limit / n) * i
			end := start + (limit / n)
			// Calculate the intermediate sum.
			for j := start; j < end; j += 1 {
				sum += j
			}
			// Pass the final sum into the channel.
			r <- sum
			// Call the goroutine and pass the CPU index and the channel.
		}(i, res)
	}

	sum := 0
	// This loop reads `n` values from the channel. We know exactly how many elements we will receive through the channel, hence we need no
	for i := 0; i < n; i++ {
		// Read a value from the channel and add it to `sum`.
		//
		//  The channel blocks when there are no elements to read. This provides a "natural" synchronization mechanism. The loop must wait until there is an element to read, and does not finish before all `n` elements have been passed through the channel.
		sum += <-res
	}
	return sum
}

/*
After adding a third benchmark function, `BenchmarkChannelSum`, to our test file, we can now run the benchmark on all three variants of our loop.

```sh
$ go test -bench .
goos: darwin
goarch: amd64
pkg: github.com/appliedgo/concurrencyslower
BenchmarkSerialSum-4          1       6022493632 ns/op
BenchmarkConcurrentSum-4      1       15828807312 ns/op
BenchmarkChannelSum-4         1       1948465461 ns/op
PASS
ok      github.com/appliedgo/concurrencyslower  23.807s
```

Spreading the intermediate sums across individual local variables, rather than having them in a single slice, definitely helped us escaping the cache sync problem.

However, how can we be sure that the individual variables never share the same cacheline? Well, starting a new goroutine allocates between 2KB and 8KB of data on the stack, which is way more than the typical cacheline size of 64 bytes. And since the intermediate sum variable is not referenced from anywhere outside the goroutine that creates it, it does not escape to the heap (where it could end up near to one of the other intermediate sum variables). So we can be pretty sure that no two intermediate sum variables will end up in the same cacheline.



## How to get and run the code

Step 1: `go get` the code. Note the `-d` flag that prevents auto-installing
the binary into `$GOPATH/bin`.

    go get -d github.com/appliedgo/concurrencyslower

Step 2: `cd` to the source code directory.

    cd $GOPATH/src/github.com/appliedgo/concurrencyslower

Step 3. Run the benchmark.

    go test -bench .


## Odds and ends

Future CPU architectures and/or future Go runtimes might be able to alleviate this problem. so if you run this code, the benchmarks might not show the same effect as in this article. In this case, consider yourself lucky.

In general, it is not a good idea to have goroutines update a global variable. Remember the Go proverb: *Don't communicate by sharing memory, share memory by communicating.*


## Links

[Cache coherence](https://en.wikipedia.org/wiki/Cache_coherence) on Wikipedia


**Happy coding!**

*/
