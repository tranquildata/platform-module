/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

#include <stdio.h>
#include <stdlib.h>
#include "output.h"

static const char INPUT_PATH[] = "/moduleio/input";
static const char OUTPUT_DIR[] = "/moduleio/output";

int produce_output(const char* output_name, const char* output_content) {
  char outputPath[100];
  snprintf(outputPath, 100, "%s/%s", OUTPUT_DIR, output_name);
  FILE *outfile = fopen(outputPath, "w");
  if (outfile == NULL) {
    return -1;
  }
  int retval = 0;
  if (fputs(output_content, outfile) == EOF) {
    retval = -1;
  }
  fclose(outfile);
  return retval;
}

int main() {
  FILE *infile = fopen(INPUT_PATH, "r");
  if (infile == NULL) {
    return -1;
  }
  int ch = fgetc(infile);
  while (ch != EOF) {
    // do something in a real system 
    ch = fgetc(infile);
  }
  fclose(infile);
  // produce output
  int o = produce_output("command.csv", SAMPLE_COMMAND);
  if (o != 0) {
    return 0;
  }
  o = produce_output("housekeeping.csv", SAMPLE_HOUSEKEEPING);
  if (o != 0) {
    return o;
  }
  o = produce_output("output.csv", SAMPLE_OUTPUT);
  if (o != 0) {
    return o;
  }
  return 0;
}
