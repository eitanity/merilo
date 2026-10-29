package rollup_test

import (
	"fmt"

	"code.eitanity.com/merilo/examples/bom/rollup"
)

// A bicycle: the frame and seat are bought in, each wheel is built from a
// rim, a tyre, 32 spokes and a hub, and each hub holds two bearings. Values
// are in cents, and an assembly's value is the labour to put it together.
func Example() {
	const (
		bicycle = iota
		frame
		seat
		wheel
		rim
		tyre
		spoke
		hub
		bearing
	)
	bill := rollup.Bill{Top: bicycle, Parts: []rollup.Part{
		bicycle: {Name: "bicycle", Value: 4000, Lines: []rollup.Line{{Part: frame, Quantity: 1}, {Part: seat, Quantity: 1}, {Part: wheel, Quantity: 2}}},
		frame:   {Name: "frame", Value: 12000},
		seat:    {Name: "seat", Value: 2500},
		wheel:   {Name: "wheel", Value: 1500, Lines: []rollup.Line{{Part: rim, Quantity: 1}, {Part: tyre, Quantity: 1}, {Part: spoke, Quantity: 32}, {Part: hub, Quantity: 1}}},
		rim:     {Name: "rim", Value: 3000},
		tyre:    {Name: "tyre", Value: 1800},
		spoke:   {Name: "spoke", Value: 40},
		hub:     {Name: "hub", Value: 600, Lines: []rollup.Line{{Part: bearing, Quantity: 2}}},
		bearing: {Name: "bearing", Value: 350},
	}}
	r, err := rollup.Run(bill, 1000)
	if err != nil {
		fmt.Println(err)
		return
	}
	for i, p := range bill.Parts {
		fmt.Printf("%-8s need %3v  subtotal %6v\n", p.Name, r.Need[i], r.Subtotal[i])
	}
	fmt.Println("total", r.Total)
	// Output:
	// bicycle  need   1  subtotal  36260
	// frame    need   1  subtotal  12000
	// seat     need   1  subtotal   2500
	// wheel    need   2  subtotal   8880
	// rim      need   2  subtotal   3000
	// tyre     need   2  subtotal   1800
	// spoke    need  64  subtotal     40
	// hub      need   2  subtotal   1300
	// bearing  need   4  subtotal    350
	// total 36260
}
