#!/usr/bin/env python3
"""
Convert timeline JSON from old format to new format.

Old format (from ns-3 or other sources):
  - "timestamp" field
  - "pdr_ue_ran" and "pdr_ran_5g" as percentages (0-100)

New format (required by ntn-emulator):
  - "time" field
  - "pdr" as ratio (0.0-1.0)
"""

import json
import sys
import argparse

def convert_timeline(input_data):
    """Convert timeline events to new format."""
    converted = []
    
    for event in input_data:
        new_event = {}
        
        # Convert timestamp -> time
        if "timestamp" in event:
            new_event["time"] = float(event["timestamp"])
        elif "time" in event:
            new_event["time"] = float(event["time"])
        else:
            print(f"Warning: Event missing 'time' or 'timestamp' field: {event}", file=sys.stderr)
            continue
        
        # Copy satellite (required even if ignored in single-RAN mode)
        if "satellite" in event:
            new_event["satellite"] = event["satellite"]
        else:
            new_event["satellite"] = "UNKNOWN"
        
        # Copy delays
        if "delay_ue_ran" in event:
            new_event["delay_ue_ran"] = float(event["delay_ue_ran"])
        else:
            new_event["delay_ue_ran"] = 0.0
            
        if "delay_ran_5g" in event:
            new_event["delay_ran_5g"] = float(event["delay_ran_5g"])
        else:
            new_event["delay_ran_5g"] = 0.0
        
        # Convert PDR from percentage to ratio
        if "pdr" in event:
            # Already in correct format (0.0-1.0)
            pdr_value = float(event["pdr"])
            if pdr_value > 1.0:
                # Likely percentage (0-100), convert to ratio
                new_event["pdr"] = pdr_value / 100.0
            else:
                new_event["pdr"] = pdr_value
        elif "pdr_ue_ran" in event and "pdr_ran_5g" in event:
            # Use average of both links, convert from percentage
            pdr_ue = float(event["pdr_ue_ran"])
            pdr_5g = float(event["pdr_ran_5g"])
            avg_pdr = (pdr_ue + pdr_5g) / 2.0
            # Convert percentage to ratio
            new_event["pdr"] = avg_pdr / 100.0 if avg_pdr > 1.0 else avg_pdr
        elif "pdr_ue_ran" in event:
            pdr_value = float(event["pdr_ue_ran"])
            new_event["pdr"] = pdr_value / 100.0 if pdr_value > 1.0 else pdr_value
        elif "pdr_ran_5g" in event:
            pdr_value = float(event["pdr_ran_5g"])
            new_event["pdr"] = pdr_value / 100.0 if pdr_value > 1.0 else pdr_value
        else:
            # Default: perfect delivery
            new_event["pdr"] = 1.0
        
        # Copy event type if present
        if "event" in event:
            new_event["event"] = event["event"]
        
        converted.append(new_event)
    
    return converted

def main():
    parser = argparse.ArgumentParser(
        description="Convert timeline JSON to ntn-emulator format",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Convert and print to stdout
  ./convert_timeline.py ntn_timeline.json
  
  # Convert and save to file
  ./convert_timeline.py ntn_timeline.json -o ntn_timeline_converted.json
  
  # Read from stdin
  cat timeline.json | ./convert_timeline.py -
        """
    )
    parser.add_argument("input", help="Input timeline JSON file (use '-' for stdin)")
    parser.add_argument("-o", "--output", help="Output file (default: stdout)")
    parser.add_argument("--pretty", action="store_true", help="Pretty-print JSON output")
    
    args = parser.parse_args()
    
    # Read input
    try:
        if args.input == "-":
            input_data = json.load(sys.stdin)
        else:
            with open(args.input, 'r') as f:
                input_data = json.load(f)
    except Exception as e:
        print(f"Error reading input: {e}", file=sys.stderr)
        sys.exit(1)
    
    # Convert
    try:
        converted_data = convert_timeline(input_data)
    except Exception as e:
        print(f"Error converting timeline: {e}", file=sys.stderr)
        sys.exit(1)
    
    # Write output
    try:
        json_kwargs = {"indent": 2} if args.pretty else {}
        json_output = json.dumps(converted_data, **json_kwargs)
        
        if args.output:
            with open(args.output, 'w') as f:
                f.write(json_output)
                if args.pretty:
                    f.write('\n')
            print(f"✓ Converted timeline written to {args.output}", file=sys.stderr)
        else:
            print(json_output)
    except Exception as e:
        print(f"Error writing output: {e}", file=sys.stderr)
        sys.exit(1)

if __name__ == "__main__":
    main()
